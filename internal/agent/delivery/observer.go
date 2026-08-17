package delivery

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	credentialURLPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@[:space:]]+@`)
	secretPairPattern    = regexp.MustCompile(`(?i)\b(password|passwd|token|authorization|bearer|client_secret)([=:][[:space:]]*)([^[:space:],;]+)`)
	digestValuePattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Observation is an informer snapshot containing only the two Flux objects
// owned by one accepted assignment. It is pure input to NormalizeObservation.
type Observation struct {
	Assignment protocol.DeliveryAssignmentV2
	Source     *unstructured.Unstructured
	Reconciler *unstructured.Unstructured
	ObservedAt time.Time
}

// AcceptedAssignment is the credential-free, values-free identity persisted by
// the agent after an assignment applies successfully. It contains exactly what
// is required to rebuild observation and deletion state after a process restart;
// source URLs, Helm values, patches, substitutions, and credentials never enter
// the checkpoint ConfigMap.
type AcceptedAssignment struct {
	DeploymentID     string                        `json:"deployment_id"`
	ProjectID        string                        `json:"project_id"`
	Generation       int64                         `json:"generation"`
	SpecDigest       string                        `json:"spec_digest"`
	Action           protocol.DeliveryAction       `json:"action"`
	Scope            protocol.DeliveryScope        `json:"scope"`
	SourceKind       protocol.DeliverySourceKind   `json:"source_kind"`
	RendererKind     protocol.DeliveryRendererKind `json:"renderer_kind"`
	ControlNamespace string                        `json:"control_namespace"`
	TargetNamespace  string                        `json:"target_namespace"`
	Objects          []ObjectIdentity              `json:"objects"`
}

type AcceptedObservation struct {
	Assignment AcceptedAssignment
	Source     *unstructured.Unstructured
	Reconciler *unstructured.Unstructured
	ObservedAt time.Time
}

func AcceptAssignment(assignment protocol.DeliveryAssignmentV2, materialization Materialization) AcceptedAssignment {
	return AcceptedAssignment{
		DeploymentID:     assignment.DeploymentID,
		ProjectID:        assignment.ProjectID,
		Generation:       assignment.Generation,
		SpecDigest:       assignment.SpecDigest,
		Action:           assignment.Action,
		Scope:            assignment.Scope,
		SourceKind:       assignment.Source.Kind,
		RendererKind:     assignment.Renderer.Kind,
		ControlNamespace: materialization.ControlNamespace,
		TargetNamespace:  materialization.TargetNamespace,
		Objects:          materializationIdentities(materialization),
	}
}

func (a AcceptedAssignment) boundaryAssignment() protocol.DeliveryAssignmentV2 {
	return protocol.DeliveryAssignmentV2{
		DeploymentID: a.DeploymentID,
		ProjectID:    a.ProjectID,
		Generation:   a.Generation,
		SpecDigest:   a.SpecDigest,
		Action:       a.Action,
		Scope:        a.Scope,
	}
}

func (a AcceptedAssignment) materializationBoundary() Materialization {
	return Materialization{ControlNamespace: a.ControlNamespace, TargetNamespace: a.TargetNamespace}
}

// NormalizeObservation converts Flux status to the bounded, credential-free
// protocol projection. It never copies spec, Secret, event, or arbitrary object
// fields into status.
func NormalizeObservation(observation Observation) (protocol.DeliveryDeploymentStatusV2, error) {
	if err := observation.Assignment.Validate(); err != nil {
		return protocol.DeliveryDeploymentStatusV2{}, fmt.Errorf("assignment: %w", err)
	}
	accepted := AcceptedAssignment{
		DeploymentID: observation.Assignment.DeploymentID,
		ProjectID:    observation.Assignment.ProjectID,
		Generation:   observation.Assignment.Generation,
		SpecDigest:   observation.Assignment.SpecDigest,
		Action:       observation.Assignment.Action,
		Scope:        observation.Assignment.Scope,
		SourceKind:   observation.Assignment.Source.Kind,
		RendererKind: observation.Assignment.Renderer.Kind,
	}
	return NormalizeAcceptedObservation(AcceptedObservation{
		Assignment: accepted,
		Source:     observation.Source, Reconciler: observation.Reconciler, ObservedAt: observation.ObservedAt,
	})
}

func NormalizeAcceptedObservation(observation AcceptedObservation) (protocol.DeliveryDeploymentStatusV2, error) {
	if err := validateAcceptedAssignment(observation.Assignment); err != nil {
		return protocol.DeliveryDeploymentStatusV2{}, fmt.Errorf("accepted assignment: %w", err)
	}
	if observation.ObservedAt.IsZero() {
		return protocol.DeliveryDeploymentStatusV2{}, fmt.Errorf("observation time is required")
	}
	if err := validateAcceptedObservedObject(observation.Assignment, observation.Source, true); err != nil {
		return protocol.DeliveryDeploymentStatusV2{}, err
	}
	if err := validateAcceptedObservedObject(observation.Assignment, observation.Reconciler, false); err != nil {
		return protocol.DeliveryDeploymentStatusV2{}, err
	}

	sourceConditions := normalizeConditions("Source", observation.Source)
	reconcilerConditions := normalizeConditions("Reconciler", observation.Reconciler)
	conditions := append(sourceConditions, reconcilerConditions...)
	sort.Slice(conditions, func(i, j int) bool {
		if conditions[i].Type != conditions[j].Type {
			return conditions[i].Type < conditions[j].Type
		}
		if conditions[i].Status != conditions[j].Status {
			return conditions[i].Status < conditions[j].Status
		}
		return conditions[i].Reason < conditions[j].Reason
	})

	revision, digest := observedArtifact(observation.Source)
	inventory := observedInventory(observation.Reconciler)
	phase := acceptedObservationPhase(observation, sourceConditions, reconcilerConditions)
	errorCode, warnings := acceptedObservationDiagnostics(observation, sourceConditions, reconcilerConditions)
	names := Names(observation.Assignment.ProjectID, observation.Assignment.DeploymentID)
	if phase == "ready" {
		inventory.Ready = inventory.Entries
	}
	if phase == "failed" || phase == "degraded" {
		inventory.Failed = inventory.Entries
	}
	return protocol.DeliveryDeploymentStatusV2{
		DeploymentID:     observation.Assignment.DeploymentID,
		Generation:       observation.Assignment.Generation,
		SpecDigest:       observation.Assignment.SpecDigest,
		Phase:            phase,
		ObservedRevision: revision,
		ObservedDigest:   digest,
		SourceKind:       sourceKind(observation.Assignment.SourceKind),
		SourceName:       names.Source,
		ReconcilerKind:   reconcilerKind(observation.Assignment.RendererKind),
		ReconcilerName:   names.Base,
		ErrorCode:        errorCode,
		WarningCodes:     warnings,
		Conditions:       conditions,
		Inventory:        inventory,
		ObservedAt:       observation.ObservedAt.UTC(),
	}, nil
}

func validateAcceptedAssignment(assignment AcceptedAssignment) error {
	deploymentID, deploymentErr := uuid.Parse(assignment.DeploymentID)
	projectID, projectErr := uuid.Parse(assignment.ProjectID)
	if deploymentErr != nil || projectErr != nil || deploymentID.String() != assignment.DeploymentID || projectID.String() != assignment.ProjectID ||
		assignment.Generation < 1 || !digestValuePattern.MatchString(assignment.SpecDigest) {
		return fmt.Errorf("identity, generation, and spec digest are required")
	}
	if assignment.Action != protocol.DeliveryActionApply && assignment.Action != protocol.DeliveryActionSuspend {
		return fmt.Errorf("unsupported action %q", assignment.Action)
	}
	if assignment.Scope != protocol.DeliveryScopeNamespace && assignment.Scope != protocol.DeliveryScopePlatform {
		return fmt.Errorf("unsupported scope %q", assignment.Scope)
	}
	switch assignment.SourceKind {
	case protocol.DeliverySourceGit, protocol.DeliverySourceOCIArtifact, protocol.DeliverySourceHelmHTTP, protocol.DeliverySourceHelmOCI:
	default:
		return fmt.Errorf("unsupported source kind %q", assignment.SourceKind)
	}
	if assignment.RendererKind != protocol.DeliveryRendererKustomize && assignment.RendererKind != protocol.DeliveryRendererHelm {
		return fmt.Errorf("unsupported source or renderer kind")
	}
	return nil
}

func reconcilerKind(kind protocol.DeliveryRendererKind) string {
	if kind == protocol.DeliveryRendererKustomize {
		return "Kustomization"
	}
	return "HelmRelease"
}

func acceptedObservationDiagnostics(observation AcceptedObservation, source, reconciler []protocol.DeliveryCondition) (string, []string) {
	if conditionTrue(source, "SourceStalled") {
		return "source_stalled", nil
	}
	if conditionTrue(reconciler, "ReconcilerStalled") {
		return "reconciler_stalled", nil
	}
	warnings := make([]string, 0, 3)
	if observation.Source == nil {
		warnings = append(warnings, "source_missing")
	}
	if observation.Reconciler == nil {
		warnings = append(warnings, "reconciler_missing")
	}
	if objectGenerationLagging(observation.Source) || objectGenerationLagging(observation.Reconciler) {
		warnings = append(warnings, "generation_lag")
	}
	return "", warnings
}

func validateAcceptedObservedObject(assignment AcceptedAssignment, object *unstructured.Unstructured, source bool) error {
	if object == nil {
		return nil
	}
	labels := object.GetLabels()
	if labels[ManagedByLabel] != ManagedByValue || labels[DeploymentIDLabel] != assignment.DeploymentID || labels[ProjectIDHashLabel] != projectHash(assignment.ProjectID) {
		return fmt.Errorf("observed object %s is outside the accepted assignment boundary", Identity(object))
	}
	annotations := object.GetAnnotations()
	if annotations[SpecDigestAnnotation] != assignment.SpecDigest {
		return fmt.Errorf("observed object %s has a stale spec digest", Identity(object))
	}
	if source {
		switch object.GetKind() {
		case "GitRepository", "OCIRepository", "HelmRepository":
		default:
			return fmt.Errorf("observed source has unsupported kind %q", object.GetKind())
		}
	} else {
		switch object.GetKind() {
		case "Kustomization", "HelmRelease":
		default:
			return fmt.Errorf("observed reconciler has unsupported kind %q", object.GetKind())
		}
	}
	return nil
}

func normalizeConditions(prefix string, object *unstructured.Unstructured) []protocol.DeliveryCondition {
	if object == nil {
		return nil
	}
	raw, found, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	if !found {
		return nil
	}
	result := make([]protocol.DeliveryCondition, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		condition, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		conditionType, _ := condition["type"].(string)
		status, _ := condition["status"].(string)
		if !allowedConditionType(conditionType) || (status != "True" && status != "False" && status != "Unknown") {
			continue
		}
		normalizedType := prefix + conditionType
		if _, duplicate := seen[normalizedType]; duplicate {
			continue
		}
		seen[normalizedType] = struct{}{}
		normalized := protocol.DeliveryCondition{
			Type:    normalizedType,
			Status:  status,
			Reason:  sanitizeStatusText(stringValue(condition["reason"]), 256),
			Message: sanitizeStatusText(stringValue(condition["message"]), protocol.MaxDeliveryStatusMessageBytes),
		}
		normalized.ObservedGeneration = int64Value(condition["observedGeneration"])
		if value := stringValue(condition["lastTransitionTime"]); value != "" {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				normalized.LastTransitionTime = parsed.UTC()
			}
		}
		result = append(result, normalized)
	}
	return result
}

func allowedConditionType(value string) bool {
	switch value {
	case "Ready", "Reconciling", "Stalled", "Healthy", "Released", "Remediated", "ArtifactInStorage", "SourceVerified":
		return true
	default:
		return false
	}
}

func acceptedObservationPhase(observation AcceptedObservation, source, reconciler []protocol.DeliveryCondition) string {
	if observation.Assignment.Action == protocol.DeliveryActionSuspend {
		return "suspended"
	}
	if observation.Source == nil || observation.Reconciler == nil {
		return "pending"
	}
	if conditionTrue(source, "SourceStalled") || conditionTrue(reconciler, "ReconcilerStalled") {
		return "failed"
	}
	if objectGenerationLagging(observation.Source) || objectGenerationLagging(observation.Reconciler) ||
		conditionTrue(source, "SourceReconciling") || conditionTrue(reconciler, "ReconcilerReconciling") {
		return "applying"
	}
	if conditionTrue(source, "SourceReady") && conditionTrue(reconciler, "ReconcilerReady") {
		return "ready"
	}
	if conditionFalse(source, "SourceReady") || conditionFalse(reconciler, "ReconcilerReady") {
		return "degraded"
	}
	return "pending"
}

func conditionTrue(conditions []protocol.DeliveryCondition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.Status == "True" {
			return true
		}
	}
	return false
}

func conditionFalse(conditions []protocol.DeliveryCondition, conditionType string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && condition.Status == "False" {
			return true
		}
	}
	return false
}

func objectGenerationLagging(object *unstructured.Unstructured) bool {
	if object == nil || object.GetGeneration() < 1 {
		return false
	}
	conditions, found, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	if !found {
		return true
	}
	var maxObserved int64
	for _, raw := range conditions {
		if entry, ok := raw.(map[string]any); ok {
			if observed := int64Value(entry["observedGeneration"]); observed > maxObserved {
				maxObserved = observed
			}
		}
	}
	return maxObserved < object.GetGeneration()
}

func observedArtifact(source *unstructured.Unstructured) (string, string) {
	if source == nil {
		return "", ""
	}
	revision, _, _ := unstructured.NestedString(source.Object, "status", "artifact", "revision")
	digest, _, _ := unstructured.NestedString(source.Object, "status", "artifact", "digest")
	revision = sanitizeStatusText(revision, 2048)
	if !digestValuePattern.MatchString(digest) {
		digest = ""
	}
	return revision, digest
}

func observedInventory(reconciler *unstructured.Unstructured) protocol.DeliveryInventory {
	if reconciler == nil {
		return protocol.DeliveryInventory{}
	}
	entries, found, _ := unstructured.NestedSlice(reconciler.Object, "status", "inventory", "entries")
	if !found {
		return protocol.DeliveryInventory{}
	}
	return protocol.DeliveryInventory{Entries: len(entries)}
}

func sanitizeStatusText(value string, limit int) string {
	if value == "" || limit <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, "�")
	value = credentialURLPattern.ReplaceAllString(value, "${1}[redacted]@")
	value = secretPairPattern.ReplaceAllString(value, "$1$2[redacted]")
	if index := strings.Index(value, "-----BEGIN "); index >= 0 {
		value = value[:index] + "[redacted pem]"
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// CoalesceStatuses keeps only the newest observation for each deployment and
// returns a stable full snapshot. Conflicting equal-time observations fail
// closed instead of depending on informer delivery order.
func CoalesceStatuses(statuses []protocol.DeliveryDeploymentStatusV2) ([]protocol.DeliveryDeploymentStatusV2, error) {
	if len(statuses) > protocol.MaxDeliveryStatusDeployments*4 {
		return nil, fmt.Errorf("status coalescing input exceeds bounded event window")
	}
	latest := make(map[string]protocol.DeliveryDeploymentStatusV2, len(statuses))
	for _, status := range statuses {
		current, exists := latest[status.DeploymentID]
		if !exists || status.ObservedAt.After(current.ObservedAt) ||
			(status.ObservedAt.Equal(current.ObservedAt) && status.Generation > current.Generation) {
			latest[status.DeploymentID] = status
			continue
		}
		if status.ObservedAt.Equal(current.ObservedAt) && status.Generation == current.Generation &&
			(status.SpecDigest != current.SpecDigest || status.Phase != current.Phase) {
			return nil, fmt.Errorf("conflicting equal-time observations for deployment %q", status.DeploymentID)
		}
	}
	if len(latest) > protocol.MaxDeliveryStatusDeployments {
		return nil, fmt.Errorf("coalesced status exceeds protocol deployment limit")
	}
	result := make([]protocol.DeliveryDeploymentStatusV2, 0, len(latest))
	for _, status := range latest {
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeploymentID < result[j].DeploymentID })
	return result, nil
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int32:
		return int64(typed)
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}
