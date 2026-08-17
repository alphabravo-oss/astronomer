// Package placement implements deterministic, side-effect-free cluster
// selection. Both preview and rollout creation must call Evaluate with data read
// from the same kind of transaction snapshot.
package placement

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/labels"

	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

type ErrorCode string

const (
	CodeInvalidInput             ErrorCode = "invalid_input"
	CodeDuplicateClusterConflict ErrorCode = "duplicate_cluster_conflict"
	CodePreviewStale             ErrorCode = "preview_stale"
	CodeAllConfirmationRequired  ErrorCode = "all_clusters_confirmation_required"
)

type Error struct {
	Code      ErrorCode
	ClusterID uuid.UUID
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return "placement failed"
	}
	if e.ClusterID != uuid.Nil {
		return fmt.Sprintf("placement %s for cluster %s: %v", e.Code, e.ClusterID, e.Cause)
	}
	return fmt.Sprintf("placement %s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

func HasCode(err error, code ErrorCode) bool {
	var placementError *Error
	return errors.As(err, &placementError) && placementError.Code == code
}

type CompatibilityStatus string

const (
	CompatibilityCompatible   CompatibilityStatus = "compatible"
	CompatibilityIncompatible CompatibilityStatus = "incompatible"
	CompatibilityUnknown      CompatibilityStatus = "unknown"
)

// Candidate is the complete batch projection required by the planner. No
// planner implementation may query per cluster.
type Candidate struct {
	ID                  uuid.UUID
	ProjectID           uuid.UUID
	Name                string
	Labels              map[string]string
	GroupIDs            []uuid.UUID
	Capabilities        map[string]string // qualified capability -> semantic version, or blank for a feature bit
	Connected           bool
	Compatibility       CompatibilityStatus
	CompatibilityReason string // bounded machine code, never raw diagnostics
	Decommissioning     bool
}

type SnapshotIdentity struct {
	TargetGeneration uint64                  `json:"target_generation"`
	BundleVersionID  uuid.UUID               `json:"bundle_version_id"`
	BundleSpecDigest model.Digest            `json:"bundle_spec_digest"`
	ResolvedRevision model.ImmutableRevision `json:"resolved_revision"`
}

type Request struct {
	Placement            model.Placement
	AllowedProjectIDs    []uuid.UUID
	Candidates           []Candidate
	RequiredCapabilities []model.CapabilityRequirement
	Identity             SnapshotIdentity
}

type DecisionReason string

const (
	ReasonSelected           DecisionReason = "selected"
	ReasonExcludedSelector   DecisionReason = "excluded_by_selector"
	ReasonExcludedExplicitly DecisionReason = "excluded_explicitly"
	ReasonUnauthorized       DecisionReason = "unauthorized"
	ReasonDisconnected       DecisionReason = "disconnected"
	ReasonIncompatible       DecisionReason = "incompatible"
	ReasonMissingCapability  DecisionReason = "missing_capability"
	ReasonDecommissioning    DecisionReason = "decommissioning"
)

type MatchReason string

const (
	MatchExplicitCluster MatchReason = "explicit_cluster"
	MatchAllClusters     MatchReason = "all_clusters"
	MatchClusterGroup    MatchReason = "cluster_group"
	MatchLabels          MatchReason = "match_labels"
	MatchExpressions     MatchReason = "match_expressions"
)

// Decision contains a single final disposition plus positive match details.
// MissingCapabilities is populated only for that final disposition.
type Decision struct {
	ClusterID           uuid.UUID      `json:"cluster_id"`
	ProjectID           uuid.UUID      `json:"project_id,omitempty"`
	ClusterName         string         `json:"cluster_name,omitempty"`
	Reason              DecisionReason `json:"reason"`
	MatchReasons        []MatchReason  `json:"match_reasons,omitempty"`
	MatchedGroupIDs     []uuid.UUID    `json:"matched_group_ids,omitempty"`
	MissingCapabilities []string       `json:"missing_capabilities,omitempty"`
	CompatibilityReason string         `json:"compatibility_reason,omitempty"`
}

type Result struct {
	Decisions               []Decision   `json:"decisions"`
	Selected                []Candidate  `json:"-"`
	SelectedCount           int          `json:"selected_count"`
	ExcludedCount           int          `json:"excluded_count"`
	RequiresAllConfirmation bool         `json:"requires_all_confirmation"`
	PreviewDigest           model.Digest `json:"preview_digest"`
}

// ValidateLaunch binds a launch to the exact authoritative preview and enforces
// the second confirmation required by an all-clusters placement. The planner
// should re-run Evaluate in its write transaction before calling this method.
func (r Result) ValidateLaunch(expected model.Digest, confirmAllClusters bool) error {
	if err := expected.Validate(); err != nil {
		return invalidInput(fmt.Errorf("expected preview digest: %w", err))
	}
	if r.PreviewDigest != expected {
		return &Error{Code: CodePreviewStale, Cause: errors.New("placement preview no longer matches")}
	}
	if r.RequiresAllConfirmation && !confirmAllClusters {
		return &Error{Code: CodeAllConfirmationRequired, Cause: errors.New("all-clusters placement requires explicit confirmation")}
	}
	return nil
}

// Evaluate returns a stable UUID-ordered decision set and a digest that binds
// the selector, target generation, immutable bundle/source identity, required
// capabilities, and every disposition. Candidate input ordering is irrelevant.
func Evaluate(request Request) (result Result, err error) {
	started := time.Now()
	defer func() {
		reasons := make([]string, 0, len(result.Decisions))
		for _, decision := range result.Decisions {
			reasons = append(reasons, string(decision.Reason))
		}
		deliverymetrics.ObservePlacement(placementMetricResult(err), time.Since(started), len(request.Candidates), reasons)
	}()
	canonicalPlacement, err := request.Placement.Canonical()
	if err != nil {
		return Result{}, invalidInput(err)
	}
	capabilities, err := model.CapabilityRequirementsCanonical(request.RequiredCapabilities)
	if err != nil {
		return Result{}, invalidInput(err)
	}
	if err := request.Identity.validate(); err != nil {
		return Result{}, invalidInput(err)
	}
	allowedProjects, err := canonicalUUIDSet("allowed_project_ids", request.AllowedProjectIDs)
	if err != nil {
		return Result{}, invalidInput(err)
	}
	candidates, err := canonicalCandidates(request.Candidates)
	if err != nil {
		return Result{}, err
	}

	explicit := idSet(canonicalPlacement.ClusterIDs)
	excluded := idSet(canonicalPlacement.ExcludeClusterIDs)
	projectScope := idSet(canonicalPlacement.ProjectIDs)
	groupScope := idSet(canonicalPlacement.ClusterGroupIDs)
	seenExplicit := make(map[uuid.UUID]struct{}, len(explicit))

	result = Result{RequiresAllConfirmation: canonicalPlacement.AllClusters}
	for _, candidate := range candidates {
		_, isExplicit := explicit[candidate.ID]
		if isExplicit {
			seenExplicit[candidate.ID] = struct{}{}
		}
		if _, authorized := allowedProjects[candidate.ProjectID]; !authorized {
			// Do not disclose clusters in inaccessible projects unless the caller
			// supplied that exact ID. The response confirms no information beyond
			// the caller's own input.
			if isExplicit {
				result.Decisions = append(result.Decisions, Decision{ClusterID: candidate.ID, Reason: ReasonUnauthorized})
			}
			continue
		}

		decision := Decision{ClusterID: candidate.ID, ProjectID: candidate.ProjectID, ClusterName: candidate.Name}
		if _, isExcluded := excluded[candidate.ID]; isExcluded {
			decision.Reason = ReasonExcludedExplicitly
			result.Decisions = append(result.Decisions, decision)
			continue
		}

		matched, reasons, groups := matches(canonicalPlacement, projectScope, groupScope, explicit, candidate)
		decision.MatchReasons = reasons
		decision.MatchedGroupIDs = groups
		if !matched {
			decision.Reason = ReasonExcludedSelector
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		if candidate.Decommissioning {
			decision.Reason = ReasonDecommissioning
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		if !candidate.Connected {
			decision.Reason = ReasonDisconnected
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		if candidate.Compatibility != CompatibilityCompatible {
			decision.Reason = ReasonIncompatible
			decision.CompatibilityReason = candidate.CompatibilityReason
			if decision.CompatibilityReason == "" {
				decision.CompatibilityReason = "compatibility_unknown"
			}
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		missing := missingCapabilities(capabilities, candidate.Capabilities)
		if len(missing) != 0 {
			decision.Reason = ReasonMissingCapability
			decision.MissingCapabilities = missing
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		decision.Reason = ReasonSelected
		result.Decisions = append(result.Decisions, decision)
		result.Selected = append(result.Selected, cloneCandidate(candidate))
	}

	// A requested ID absent from the batch projection is deliberately reported
	// as unauthorized. This avoids exposing whether it is nonexistent, deleted,
	// or merely outside the caller's scope.
	for id := range explicit {
		if _, seen := seenExplicit[id]; !seen {
			result.Decisions = append(result.Decisions, Decision{ClusterID: id, Reason: ReasonUnauthorized})
		}
	}
	sort.Slice(result.Decisions, func(i, j int) bool {
		return result.Decisions[i].ClusterID.String() < result.Decisions[j].ClusterID.String()
	})
	result.SelectedCount = len(result.Selected)
	result.ExcludedCount = len(result.Decisions) - result.SelectedCount

	digestInput := struct {
		Placement            model.Placement               `json:"placement"`
		RequiredCapabilities []model.CapabilityRequirement `json:"required_capabilities,omitempty"`
		Identity             SnapshotIdentity              `json:"identity"`
		Decisions            []Decision                    `json:"decisions"`
	}{
		Placement: canonicalPlacement, RequiredCapabilities: capabilities,
		Identity: request.Identity, Decisions: result.Decisions,
	}
	result.PreviewDigest, err = model.CanonicalDigest(digestInput)
	if err != nil {
		return Result{}, fmt.Errorf("digest placement preview: %w", err)
	}
	return result, nil
}

func placementMetricResult(err error) string {
	if err == nil {
		return "success"
	}
	for _, candidate := range []struct {
		code   ErrorCode
		result string
	}{
		{CodeInvalidInput, "invalid"},
		{CodeDuplicateClusterConflict, "conflict"},
		{CodePreviewStale, "stale"},
		{CodeAllConfirmationRequired, "confirmation_required"},
	} {
		if HasCode(err, candidate.code) {
			return candidate.result
		}
	}
	return "invalid"
}

func (i SnapshotIdentity) validate() error {
	if i.TargetGeneration == 0 {
		return errors.New("target generation must be greater than zero")
	}
	if i.BundleVersionID == uuid.Nil {
		return errors.New("bundle version ID must be a non-zero UUID")
	}
	if err := i.BundleSpecDigest.Validate(); err != nil {
		return fmt.Errorf("bundle spec digest: %w", err)
	}
	if err := i.ResolvedRevision.Validate(); err != nil {
		return fmt.Errorf("resolved revision: %w", err)
	}
	return nil
}

func matches(
	selector model.Placement,
	projectScope, groupScope, explicit map[uuid.UUID]struct{},
	candidate Candidate,
) (bool, []MatchReason, []uuid.UUID) {
	if len(projectScope) != 0 {
		if _, ok := projectScope[candidate.ProjectID]; !ok {
			return false, nil, nil
		}
	}
	if selector.AllClusters {
		return true, []MatchReason{MatchAllClusters}, nil
	}
	if _, ok := explicit[candidate.ID]; ok {
		return true, []MatchReason{MatchExplicitCluster}, nil
	}

	predicateConfigured := len(groupScope) != 0 || len(selector.MatchLabels) != 0 || len(selector.MatchExpressions) != 0
	if !predicateConfigured {
		return false, nil, nil
	}
	reasons := make([]MatchReason, 0, 3)
	matchedGroups := make([]uuid.UUID, 0)
	if len(groupScope) != 0 {
		for _, groupID := range candidate.GroupIDs {
			if _, ok := groupScope[groupID]; ok {
				matchedGroups = append(matchedGroups, groupID)
			}
		}
		if len(matchedGroups) == 0 {
			return false, nil, nil
		}
		reasons = append(reasons, MatchClusterGroup)
	}
	labelSet := labels.Set(candidate.Labels)
	for key, wanted := range selector.MatchLabels {
		value, exists := labelSet[key]
		if !exists || value != wanted {
			return false, nil, nil
		}
	}
	if len(selector.MatchLabels) != 0 {
		reasons = append(reasons, MatchLabels)
	}
	for _, expression := range selector.MatchExpressions {
		value, exists := labelSet[expression.Key]
		if !matchesExpression(expression, value, exists) {
			return false, nil, nil
		}
	}
	if len(selector.MatchExpressions) != 0 {
		reasons = append(reasons, MatchExpressions)
	}
	return true, reasons, matchedGroups
}

// matchesExpression mirrors k8s.io/apimachinery labels.Requirement semantics,
// including NotIn matching a missing key.
func matchesExpression(expression model.LabelExpression, value string, exists bool) bool {
	switch expression.Operator {
	case model.OperatorIn:
		return exists && contains(expression.Values, value)
	case model.OperatorNotIn:
		return !exists || !contains(expression.Values, value)
	case model.OperatorExists:
		return exists
	case model.OperatorDoesNotExist:
		return !exists
	default:
		return false
	}
}

func missingCapabilities(required []model.CapabilityRequirement, actual map[string]string) []string {
	missing := make([]string, 0)
	for _, requirement := range required {
		version, ok := actual[requirement.Name]
		if !ok {
			missing = append(missing, requirement.Name)
			continue
		}
		if requirement.Constraint == "" {
			continue
		}
		constraint, _ := semver.NewConstraint(requirement.Constraint)
		parsed, err := semver.NewVersion(version)
		if err != nil || !constraint.Check(parsed) {
			missing = append(missing, requirement.Name+"@"+requirement.Constraint)
		}
	}
	return missing
}

func canonicalCandidates(input []Candidate) ([]Candidate, error) {
	input = append([]Candidate(nil), input...)
	sort.SliceStable(input, func(i, j int) bool { return input[i].ID.String() < input[j].ID.String() })
	byID := make(map[uuid.UUID]Candidate, len(input))
	for _, raw := range input {
		candidate, err := canonicalCandidate(raw)
		if err != nil {
			return nil, invalidInput(err)
		}
		if existing, ok := byID[candidate.ID]; ok {
			merged, mergeErr := mergeCandidates(existing, candidate)
			if mergeErr != nil {
				return nil, &Error{Code: CodeDuplicateClusterConflict, ClusterID: candidate.ID, Cause: mergeErr}
			}
			byID[candidate.ID] = merged
		} else {
			byID[candidate.ID] = candidate
		}
	}
	result := make([]Candidate, 0, len(byID))
	for _, candidate := range byID {
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID.String() < result[j].ID.String() })
	return result, nil
}

func canonicalCandidate(candidate Candidate) (Candidate, error) {
	if candidate.ID == uuid.Nil || candidate.ProjectID == uuid.Nil {
		return Candidate{}, errors.New("candidate and project IDs must be non-zero UUIDs")
	}
	if strings.TrimSpace(candidate.Name) == "" || len(candidate.Name) > model.MaxNameLength {
		return Candidate{}, errors.New("candidate name must be non-empty and bounded")
	}
	placement := model.Placement{MatchLabels: candidate.Labels}
	if err := placement.Validate(); err != nil {
		return Candidate{}, fmt.Errorf("candidate labels: %w", err)
	}
	groups, err := canonicalUUIDSet("candidate group IDs", candidate.GroupIDs)
	if err != nil {
		return Candidate{}, err
	}
	if len(candidate.Capabilities) > model.MaxCapabilities {
		return Candidate{}, fmt.Errorf("candidate capabilities exceed %d entries", model.MaxCapabilities)
	}
	capabilities := make(map[string]string, len(candidate.Capabilities))
	capabilityNames := make([]string, 0, len(candidate.Capabilities))
	for name := range candidate.Capabilities {
		capabilityNames = append(capabilityNames, name)
	}
	sort.Strings(capabilityNames)
	for _, name := range capabilityNames {
		version := candidate.Capabilities[name]
		if err := (model.CapabilityRequirement{Name: name}).Validate(); err != nil {
			return Candidate{}, fmt.Errorf("candidate capability name: %w", err)
		}
		if version != "" {
			if _, err := semver.NewVersion(version); err != nil {
				return Candidate{}, fmt.Errorf("candidate capability %s has invalid version", name)
			}
		}
		capabilities[name] = version
	}
	switch candidate.Compatibility {
	case CompatibilityCompatible, CompatibilityIncompatible, CompatibilityUnknown:
	default:
		return Candidate{}, errors.New("candidate compatibility status is invalid")
	}
	if candidate.CompatibilityReason != "" && !model.SafeCode(candidate.CompatibilityReason) {
		return Candidate{}, errors.New("candidate compatibility reason must be a bounded machine code")
	}
	candidate.Labels = cloneStringMap(candidate.Labels)
	candidate.GroupIDs = setUUIDs(groups)
	candidate.Capabilities = capabilities
	return candidate, nil
}

func mergeCandidates(left, right Candidate) (Candidate, error) {
	leftGroups, rightGroups := left.GroupIDs, right.GroupIDs
	leftCaps, rightCaps := left.Capabilities, right.Capabilities
	left.GroupIDs, right.GroupIDs = nil, nil
	left.Capabilities, right.Capabilities = nil, nil
	if !reflect.DeepEqual(left, right) {
		return Candidate{}, errors.New("duplicate projections disagree on cluster attributes")
	}
	left.GroupIDs = unionUUIDs(leftGroups, rightGroups)
	left.Capabilities = cloneStringMap(leftCaps)
	for name, version := range rightCaps {
		if old, exists := left.Capabilities[name]; exists && old != version {
			return Candidate{}, fmt.Errorf("duplicate projections disagree on capability %s", name)
		}
		left.Capabilities[name] = version
	}
	return left, nil
}

func canonicalUUIDSet(field string, input []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if len(input) > model.MaxPlacementIDs {
		return nil, fmt.Errorf("%s exceeds %d entries", field, model.MaxPlacementIDs)
	}
	result := make(map[uuid.UUID]struct{}, len(input))
	for _, id := range input {
		if id == uuid.Nil {
			return nil, fmt.Errorf("%s contains a zero UUID", field)
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("%s contains duplicate %s", field, id)
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func setUUIDs(input map[uuid.UUID]struct{}) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(input))
	for id := range input {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func unionUUIDs(left, right []uuid.UUID) []uuid.UUID {
	set := idSet(left)
	for _, value := range right {
		set[value] = struct{}{}
	}
	return setUUIDs(set)
}

func idSet(input []uuid.UUID) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(input))
	for _, id := range input {
		result[id] = struct{}{}
	}
	return result
}

func cloneCandidate(input Candidate) Candidate {
	input.Labels = cloneStringMap(input.Labels)
	input.GroupIDs = append([]uuid.UUID(nil), input.GroupIDs...)
	input.Capabilities = cloneStringMap(input.Capabilities)
	return input
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func invalidInput(err error) *Error { return &Error{Code: CodeInvalidInput, Cause: err} }
