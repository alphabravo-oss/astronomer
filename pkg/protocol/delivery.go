package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DeliveryProtocolVersion       = "2.0"
	MaxDeliveryAssignments        = 10_000
	MaxDeliveryDeletions          = 10_000
	MaxDeliveryCredentialKeys     = 32
	MaxDeliveryCredentialValue    = 256 << 10
	MaxDeliveryValuesBytes        = 1 << 20
	MaxDeliveryPatchBytes         = 256 << 10
	MaxDeliveryStatusMessageBytes = 4 << 10
	MaxDeliveryStatusDeployments  = 10_000
	MaxDeliveryConditions         = 64
	MaxDeliveryInventoryEntries   = 64
)

const (
	FeatureDeliveryAssignmentsV2     = "delivery.assignments.v2"
	FeatureDeliveryStatusV2          = "delivery.status.v2"
	FeatureDeliverySystemV2          = "delivery.system.v2"
	FeatureDeliverySourceGit         = "delivery.source.git"
	FeatureDeliverySourceOCIArtifact = "delivery.source.oci_artifact"
	FeatureDeliverySourceHelmHTTP    = "delivery.source.helm_http"
	FeatureDeliverySourceHelmOCI     = "delivery.source.helm_oci"
	FeatureDeliveryRendererKustomize = "delivery.renderer.kustomize"
	FeatureDeliveryRendererHelm      = "delivery.renderer.helm"
	FeatureDeliveryNamespaceScope    = "delivery.scope.namespace"
	FeatureDeliveryPlatformScope     = "delivery.scope.platform"
)

type DeliverySourceKind string

const (
	DeliverySourceGit         DeliverySourceKind = "git"
	DeliverySourceOCIArtifact DeliverySourceKind = "oci_artifact"
	DeliverySourceHelmHTTP    DeliverySourceKind = "helm_http"
	DeliverySourceHelmOCI     DeliverySourceKind = "helm_oci"
)

type DeliveryRendererKind string

const (
	DeliveryRendererKustomize DeliveryRendererKind = "kustomize"
	DeliveryRendererHelm      DeliveryRendererKind = "helm"
)

type DeliveryAction string

const (
	DeliveryActionApply   DeliveryAction = "apply"
	DeliveryActionSuspend DeliveryAction = "suspend"
)

type DeliveryScope string

const (
	DeliveryScopeNamespace DeliveryScope = "namespace"
	DeliveryScopePlatform  DeliveryScope = "platform"
)

// DeliveryStateRequestV2 asks for the complete released assignment snapshot for
// the authenticated cluster. ClusterID is correlation data only; the server
// must bind the request to the tunnel identity.
type DeliveryStateRequestV2 struct {
	ClusterID               string                      `json:"cluster_id"`
	ProtocolVersion         string                      `json:"protocol_version"`
	AckedSnapshotGeneration int64                       `json:"acked_snapshot_generation"`
	AckedETag               string                      `json:"acked_etag,omitempty"`
	ControllerInventory     DeliveryControllerInventory `json:"controller_inventory"`
}

type DeliveryStateResponseV2 struct {
	ProtocolVersion    string                   `json:"protocol_version"`
	SnapshotGeneration int64                    `json:"snapshot_generation"`
	ETag               string                   `json:"etag"`
	NotModified        bool                     `json:"not_modified,omitempty"`
	FullSnapshot       bool                     `json:"full_snapshot"`
	System             *DeliverySystemReleaseV2 `json:"system,omitempty"`
	Assignments        []DeliveryAssignmentV2   `json:"assignments,omitempty"`
	Deletions          []DeliveryDeletionV2     `json:"deletions,omitempty"`
	CredentialEpoch    int64                    `json:"credential_epoch"`
}

type DeliveryControllerInventory struct {
	AgentVersion         string            `json:"agent_version,omitempty"`
	FluxVersion          string            `json:"flux_version,omitempty"`
	Components           map[string]string `json:"components,omitempty"`
	APIVersions          []string          `json:"api_versions,omitempty"`
	KubernetesVersion    string            `json:"kubernetes_version,omitempty"`
	DistributionDigest   string            `json:"distribution_digest,omitempty"`
	Ready                bool              `json:"ready"`
	CompatibilityMessage string            `json:"compatibility_message,omitempty"`
}

type DeliveryAssignmentV2 struct {
	DeploymentID string                       `json:"deployment_id"`
	TargetID     string                       `json:"target_id"`
	ProjectID    string                       `json:"project_id"`
	Generation   int64                        `json:"generation"`
	SpecDigest   string                       `json:"spec_digest"`
	Action       DeliveryAction               `json:"action"`
	Scope        DeliveryScope                `json:"scope"`
	Source       DeliverySourceV2             `json:"source"`
	Renderer     DeliveryRendererV2           `json:"renderer"`
	Policy       DeliveryReconciliationPolicy `json:"policy"`
	Credential   *DeliveryCredentialMaterial  `json:"credential,omitempty"`
}

// DeliveryCredentialMaterial is write-only transport material. JSON encodes
// byte slices as base64. It must never be logged, audited, persisted in status,
// or included in an assignment/spec digest. Epoch is included in the snapshot
// ETag so rotations still produce a new snapshot.
type DeliveryCredentialMaterial struct {
	Version int64             `json:"version"`
	Data    map[string][]byte `json:"data"`
}

type DeliverySourceV2 struct {
	Kind             DeliverySourceKind `json:"kind"`
	URL              string             `json:"url"`
	Revision         string             `json:"revision"`
	Digest           string             `json:"digest,omitempty"`
	Path             string             `json:"path,omitempty"`
	Chart            string             `json:"chart,omitempty"`
	Provider         string             `json:"provider,omitempty"`
	CredentialSecret string             `json:"credential_secret,omitempty"`
	TrustSecret      string             `json:"trust_secret,omitempty"`
	Verify           *DeliveryVerifyV2  `json:"verify,omitempty"`
}

type DeliveryVerifyV2 struct {
	Provider       string                 `json:"provider"`
	Mode           string                 `json:"mode,omitempty"`
	OIDCIdentities []DeliveryOIDCIdentity `json:"oidc_identities,omitempty"`
}

type DeliveryOIDCIdentity struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

type DeliveryRendererV2 struct {
	Kind      DeliveryRendererKind       `json:"kind"`
	Kustomize *DeliveryKustomizeRenderer `json:"kustomize,omitempty"`
	Helm      *DeliveryHelmRenderer      `json:"helm,omitempty"`
}

type DeliveryKustomizeRenderer struct {
	TargetNamespace string                `json:"target_namespace"`
	ServiceAccount  string                `json:"service_account"`
	Prune           bool                  `json:"prune"`
	Wait            bool                  `json:"wait"`
	Patches         []string              `json:"patches,omitempty"`
	Substitutions   map[string]string     `json:"substitutions,omitempty"`
	HealthChecks    []DeliveryHealthCheck `json:"health_checks,omitempty"`
	DependencyNames []string              `json:"dependency_names,omitempty"`
}

type DeliveryHelmRenderer struct {
	Chart              string          `json:"chart"`
	Version            string          `json:"version"`
	ReleaseName        string          `json:"release_name"`
	TargetNamespace    string          `json:"target_namespace"`
	ServiceAccount     string          `json:"service_account"`
	Values             json.RawMessage `json:"values,omitempty"`
	InstallRetries     int             `json:"install_retries"`
	UpgradeRetries     int             `json:"upgrade_retries"`
	UpgradeRemediation string          `json:"upgrade_remediation"`
	EnableTests        bool            `json:"enable_tests"`
	DriftMode          string          `json:"drift_mode"`
	DependencyNames    []string        `json:"dependency_names,omitempty"`
}

type DeliveryHealthCheck struct {
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
}

type DeliveryReconciliationPolicy struct {
	Interval      string `json:"interval"`
	RetryInterval string `json:"retry_interval,omitempty"`
	Timeout       string `json:"timeout"`
	Drift         string `json:"drift"`
	Prune         bool   `json:"prune"`
}

type DeliveryDeletionV2 struct {
	DeploymentID string `json:"deployment_id"`
	Generation   int64  `json:"generation"`
	SpecDigest   string `json:"spec_digest"`
	Orphan       bool   `json:"orphan"`
	Deadline     string `json:"deadline,omitempty"`
}

type DeliveryStatusV2 struct {
	ProtocolVersion     string                       `json:"protocol_version"`
	ClusterID           string                       `json:"cluster_id"`
	SessionSequence     int64                        `json:"session_sequence"`
	SnapshotGeneration  int64                        `json:"snapshot_generation"`
	SnapshotETag        string                       `json:"snapshot_etag,omitempty"`
	Deployments         []DeliveryDeploymentStatusV2 `json:"deployments"`
	ControllerInventory DeliveryControllerInventory  `json:"controller_inventory"`
}

type DeliveryDeploymentStatusV2 struct {
	DeploymentID     string              `json:"deployment_id"`
	Generation       int64               `json:"generation"`
	SpecDigest       string              `json:"spec_digest"`
	Phase            string              `json:"phase"`
	ObservedRevision string              `json:"observed_revision,omitempty"`
	ObservedDigest   string              `json:"observed_digest,omitempty"`
	SourceKind       string              `json:"source_kind,omitempty"`
	SourceName       string              `json:"source_name,omitempty"`
	ReconcilerKind   string              `json:"reconciler_kind,omitempty"`
	ReconcilerName   string              `json:"reconciler_name,omitempty"`
	ErrorCode        string              `json:"error_code,omitempty"`
	WarningCodes     []string            `json:"warning_codes,omitempty"`
	Message          string              `json:"message,omitempty"`
	Conditions       []DeliveryCondition `json:"conditions,omitempty"`
	Inventory        DeliveryInventory   `json:"inventory"`
	ObservedAt       time.Time           `json:"observed_at"`
}

type DeliveryCondition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	ObservedGeneration int64     `json:"observed_generation,omitempty"`
	LastTransitionTime time.Time `json:"last_transition_time,omitempty"`
}

type DeliveryInventory struct {
	Entries int `json:"entries"`
	Ready   int `json:"ready"`
	Failed  int `json:"failed"`
}

var (
	uuidPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	gitCommitPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	dnsLabelPattern   = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
	secretKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9](?:[-._A-Za-z0-9]*[A-Za-z0-9])?$`)
	helmRangePattern  = regexp.MustCompile(`[,*<>=~^|[:space:]]`)
	reasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)
)

func (r DeliveryStateRequestV2) Validate() error {
	if r.ProtocolVersion != DeliveryProtocolVersion {
		return fmt.Errorf("unsupported delivery protocol version %q", r.ProtocolVersion)
	}
	if !validUUID(r.ClusterID) {
		return errors.New("cluster ID must be a canonical UUID")
	}
	if r.AckedSnapshotGeneration < 0 {
		return errors.New("acknowledged snapshot generation cannot be negative")
	}
	if r.AckedETag != "" && !validDigest(r.AckedETag) {
		return errors.New("acknowledged ETag must be a sha256 digest")
	}
	return r.ControllerInventory.Validate()
}

func (i DeliveryControllerInventory) Validate() error {
	if len(i.AgentVersion) > 64 || len(i.FluxVersion) > 64 || len(i.KubernetesVersion) > 64 || len(i.CompatibilityMessage) > MaxDeliveryStatusMessageBytes {
		return errors.New("controller inventory contains an oversized field")
	}
	if i.DistributionDigest != "" && !validDigest(i.DistributionDigest) {
		return errors.New("controller distribution digest must be a sha256 digest")
	}
	if len(i.Components) > MaxDeliveryInventoryEntries || len(i.APIVersions) > MaxDeliveryInventoryEntries {
		return errors.New("controller inventory exceeds entry limit")
	}
	for name, version := range i.Components {
		if !validDNSLabel(name) || len(version) > 64 {
			return fmt.Errorf("controller inventory component %q is invalid", name)
		}
	}
	for _, apiVersion := range i.APIVersions {
		if apiVersion == "" || len(apiVersion) > 128 || strings.ContainsAny(apiVersion, "\r\n\x00") {
			return errors.New("controller inventory API version is invalid")
		}
	}
	return nil
}

func (s DeliveryStatusV2) Validate() error {
	if s.ProtocolVersion != DeliveryProtocolVersion {
		return fmt.Errorf("unsupported delivery protocol version %q", s.ProtocolVersion)
	}
	if !validUUID(s.ClusterID) || s.SessionSequence < 1 || s.SnapshotGeneration < 0 {
		return errors.New("delivery status has invalid identity or sequence")
	}
	if (s.SnapshotGeneration == 0 && s.SnapshotETag != "") || (s.SnapshotGeneration > 0 && !validDigest(s.SnapshotETag)) {
		return errors.New("delivery status snapshot generation and ETag must be supplied together")
	}
	if len(s.Deployments) > MaxDeliveryStatusDeployments {
		return errors.New("delivery status exceeds deployment limit")
	}
	if err := s.ControllerInventory.Validate(); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(s.Deployments))
	for index := range s.Deployments {
		deployment := s.Deployments[index]
		if !validUUID(deployment.DeploymentID) || deployment.Generation < 1 || !validDigest(deployment.SpecDigest) {
			return fmt.Errorf("deployment status %d has invalid identity", index)
		}
		if _, duplicate := seen[deployment.DeploymentID]; duplicate {
			return fmt.Errorf("duplicate deployment status %q", deployment.DeploymentID)
		}
		seen[deployment.DeploymentID] = struct{}{}
		if !validDeliveryPhase(deployment.Phase) {
			return fmt.Errorf("deployment status %d has unsupported phase %q", index, deployment.Phase)
		}
		if deployment.ObservedDigest != "" && !validDigest(deployment.ObservedDigest) {
			return fmt.Errorf("deployment status %d has invalid observed digest", index)
		}
		if (deployment.SourceKind != "" && deployment.SourceKind != "GitRepository" && deployment.SourceKind != "OCIRepository" && deployment.SourceKind != "HelmRepository") ||
			(deployment.ReconcilerKind != "" && deployment.ReconcilerKind != "Kustomization" && deployment.ReconcilerKind != "HelmRelease") ||
			(deployment.SourceName != "" && !validDNSLabel(deployment.SourceName)) ||
			(deployment.ReconcilerName != "" && !validDNSLabel(deployment.ReconcilerName)) ||
			(deployment.ErrorCode != "" && !reasonCodePattern.MatchString(deployment.ErrorCode)) ||
			len(deployment.WarningCodes) > MaxDeliveryConditions || len(deployment.Message) > MaxDeliveryStatusMessageBytes {
			return fmt.Errorf("deployment status %d has invalid Flux identity or diagnostic", index)
		}
		for _, code := range deployment.WarningCodes {
			if !reasonCodePattern.MatchString(code) {
				return fmt.Errorf("deployment status %d has invalid warning code", index)
			}
		}
		if deployment.ObservedAt.IsZero() || len(deployment.Conditions) > MaxDeliveryConditions ||
			deployment.Inventory.Entries < 0 || deployment.Inventory.Ready < 0 || deployment.Inventory.Failed < 0 ||
			deployment.Inventory.Ready+deployment.Inventory.Failed > deployment.Inventory.Entries {
			return fmt.Errorf("deployment status %d has invalid observation data", index)
		}
		for conditionIndex := range deployment.Conditions {
			condition := deployment.Conditions[conditionIndex]
			if condition.Type == "" || len(condition.Type) > 128 ||
				(condition.Status != "True" && condition.Status != "False" && condition.Status != "Unknown") ||
				len(condition.Reason) > 256 || len(condition.Message) > MaxDeliveryStatusMessageBytes ||
				condition.ObservedGeneration < 0 {
				return fmt.Errorf("deployment status %d condition %d is invalid", index, conditionIndex)
			}
		}
	}
	return nil
}

func validDeliveryPhase(phase string) bool {
	switch phase {
	case "pending", "blocked", "applying", "ready", "degraded", "failed", "suspended", "deleting", "removed", "unknown":
		return true
	default:
		return false
	}
}

func (r DeliveryStateResponseV2) Validate() error {
	if r.ProtocolVersion != DeliveryProtocolVersion {
		return fmt.Errorf("unsupported delivery protocol version %q", r.ProtocolVersion)
	}
	if r.SnapshotGeneration < 1 || r.CredentialEpoch < 0 {
		return errors.New("invalid snapshot generation or credential epoch")
	}
	if r.NotModified {
		if r.FullSnapshot || r.System != nil || len(r.Assignments) != 0 || len(r.Deletions) != 0 || r.ETag == "" {
			return errors.New("not-modified response must contain only snapshot metadata")
		}
		return nil
	}
	if !r.FullSnapshot {
		return errors.New("delivery protocol v2 requires a full snapshot")
	}
	if len(r.Assignments) > MaxDeliveryAssignments || len(r.Deletions) > MaxDeliveryDeletions {
		return errors.New("delivery snapshot exceeds object limit")
	}
	if r.System != nil {
		if err := r.System.Validate(); err != nil {
			return fmt.Errorf("system release: %w", err)
		}
	}
	seen := make(map[string]struct{}, len(r.Assignments)+len(r.Deletions))
	for i := range r.Assignments {
		if err := r.Assignments[i].Validate(); err != nil {
			return fmt.Errorf("assignment %d: %w", i, err)
		}
		if _, exists := seen[r.Assignments[i].DeploymentID]; exists {
			return fmt.Errorf("duplicate deployment %q", r.Assignments[i].DeploymentID)
		}
		seen[r.Assignments[i].DeploymentID] = struct{}{}
	}
	for i := range r.Deletions {
		d := r.Deletions[i]
		if !validUUID(d.DeploymentID) || d.Generation < 1 || !validDigest(d.SpecDigest) {
			return fmt.Errorf("deletion %d is invalid", i)
		}
		if d.Deadline != "" {
			if _, err := time.Parse(time.RFC3339, d.Deadline); err != nil {
				return fmt.Errorf("deletion %d deadline: %w", i, err)
			}
		}
		if _, exists := seen[d.DeploymentID]; exists {
			return fmt.Errorf("duplicate deployment %q", d.DeploymentID)
		}
		seen[d.DeploymentID] = struct{}{}
	}
	want, err := r.CanonicalETag()
	if err != nil {
		return err
	}
	if r.ETag == "" || r.ETag != want {
		return errors.New("delivery snapshot ETag does not match canonical content")
	}
	return nil
}

func (a DeliveryAssignmentV2) Validate() error {
	if !validUUID(a.DeploymentID) || !validUUID(a.TargetID) || !validUUID(a.ProjectID) {
		return errors.New("deployment, target, and project IDs must be canonical UUIDs")
	}
	if a.Generation < 1 || !validDigest(a.SpecDigest) {
		return errors.New("generation and spec digest are required")
	}
	if a.Action != DeliveryActionApply && a.Action != DeliveryActionSuspend {
		return fmt.Errorf("unsupported action %q", a.Action)
	}
	if a.Scope != DeliveryScopeNamespace && a.Scope != DeliveryScopePlatform {
		return fmt.Errorf("unsupported scope %q", a.Scope)
	}
	if err := a.Source.Validate(); err != nil {
		return err
	}
	if err := a.Renderer.Validate(); err != nil {
		return err
	}
	for field, value := range map[string]string{"interval": a.Policy.Interval, "timeout": a.Policy.Timeout} {
		if value == "" {
			return fmt.Errorf("policy %s is required", field)
		}
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("policy %s: %w", field, err)
		}
	}
	if a.Policy.RetryInterval != "" {
		if _, err := time.ParseDuration(a.Policy.RetryInterval); err != nil {
			return fmt.Errorf("policy retry interval: %w", err)
		}
	}
	if a.Credential != nil {
		if a.Credential.Version < 1 || len(a.Credential.Data) == 0 || len(a.Credential.Data) > MaxDeliveryCredentialKeys {
			return errors.New("credential material has invalid version or key count")
		}
		for key, value := range a.Credential.Data {
			if !validSecretKey(key) || len(value) == 0 || len(value) > MaxDeliveryCredentialValue {
				return fmt.Errorf("credential material key %q is invalid", key)
			}
		}
	}
	return nil
}

func (s DeliverySourceV2) Validate() error {
	switch s.Kind {
	case DeliverySourceGit, DeliverySourceOCIArtifact, DeliverySourceHelmHTTP, DeliverySourceHelmOCI:
	default:
		return fmt.Errorf("unsupported source kind %q", s.Kind)
	}
	parsed, err := url.Parse(s.URL)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("source URL must be an absolute credential-free URL without query or fragment")
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return errors.New("source URL must not contain a password")
		}
	}
	if s.Path != "" && (strings.HasPrefix(s.Path, "/") || pathpkg.Clean(s.Path) == ".." || strings.HasPrefix(pathpkg.Clean(s.Path), "../")) {
		return errors.New("source path must be relative and cannot traverse parents")
	}
	if s.CredentialSecret != "" && !validDNSLabel(s.CredentialSecret) {
		return errors.New("invalid source credential Secret name")
	}
	if s.TrustSecret != "" && !validDNSLabel(s.TrustSecret) {
		return errors.New("invalid source trust Secret name")
	}
	switch s.Kind {
	case DeliverySourceGit:
		if parsed.Scheme != "ssh" && parsed.Scheme != "https" {
			return errors.New("git source URL scheme must be ssh or https")
		}
		if !gitCommitPattern.MatchString(s.Revision) {
			return errors.New("git source revision must be a full immutable commit")
		}
	case DeliverySourceOCIArtifact:
		if parsed.Scheme != "oci" {
			return errors.New("OCI artifact URL scheme must be oci")
		}
		if !validDigest(s.Digest) || s.Revision != s.Digest {
			return errors.New("OCI artifact revision must equal its sha256 digest")
		}
	case DeliverySourceHelmHTTP, DeliverySourceHelmOCI:
		if (s.Kind == DeliverySourceHelmHTTP && parsed.Scheme != "https") || (s.Kind == DeliverySourceHelmOCI && parsed.Scheme != "oci") {
			return errors.New("Helm source URL scheme does not match its source kind")
		}
		if s.Chart == "" || s.Revision == "" || helmRangePattern.MatchString(s.Revision) {
			return errors.New("Helm source requires a chart and exact version")
		}
		if !validDigest(s.Digest) {
			return errors.New("Helm source requires a resolved sha256 artifact digest")
		}
	}
	return nil
}

func (r DeliveryRendererV2) Validate() error {
	switch r.Kind {
	case DeliveryRendererKustomize:
		if r.Kustomize == nil || r.Helm != nil {
			return errors.New("kustomize renderer must set only kustomize configuration")
		}
		if !validDNSLabel(r.Kustomize.TargetNamespace) || !validDNSLabel(r.Kustomize.ServiceAccount) {
			return errors.New("kustomize target namespace and service account are required")
		}
		patchBytes := 0
		for _, patch := range r.Kustomize.Patches {
			patchBytes += len(patch)
		}
		if patchBytes > MaxDeliveryPatchBytes {
			return errors.New("kustomize patches exceed size limit")
		}
	case DeliveryRendererHelm:
		if r.Helm == nil || r.Kustomize != nil {
			return errors.New("helm renderer must set only helm configuration")
		}
		if r.Helm.Chart == "" || r.Helm.Version == "" || r.Helm.ReleaseName == "" ||
			!validDNSLabel(r.Helm.TargetNamespace) || !validDNSLabel(r.Helm.ServiceAccount) {
			return errors.New("helm chart, version, release, target namespace, and service account are required")
		}
		if len(r.Helm.Values) > MaxDeliveryValuesBytes || (len(r.Helm.Values) > 0 && !json.Valid(r.Helm.Values)) {
			return errors.New("helm values must be valid bounded JSON")
		}
	default:
		return fmt.Errorf("unsupported renderer kind %q", r.Kind)
	}
	return nil
}

// CanonicalETag hashes canonical desired metadata while deliberately excluding
// credential bytes. CredentialEpoch remains part of the hash input, so rotation
// invalidates the snapshot without making secret material observable by hash.
func (r DeliveryStateResponseV2) CanonicalETag() (string, error) {
	var system *DeliverySystemReleaseV2
	if r.System != nil {
		copy := *r.System
		copy.Credential = nil
		system = &copy
	}
	assignments := append([]DeliveryAssignmentV2(nil), r.Assignments...)
	for i := range assignments {
		assignments[i].Credential = nil
	}
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].DeploymentID < assignments[j].DeploymentID })
	deletions := append([]DeliveryDeletionV2(nil), r.Deletions...)
	sort.Slice(deletions, func(i, j int) bool { return deletions[i].DeploymentID < deletions[j].DeploymentID })
	canonical := struct {
		ProtocolVersion    string                   `json:"protocol_version"`
		SnapshotGeneration int64                    `json:"snapshot_generation"`
		CredentialEpoch    int64                    `json:"credential_epoch"`
		System             *DeliverySystemReleaseV2 `json:"system,omitempty"`
		Assignments        []DeliveryAssignmentV2   `json:"assignments"`
		Deletions          []DeliveryDeletionV2     `json:"deletions"`
	}{r.ProtocolVersion, r.SnapshotGeneration, r.CredentialEpoch, system, assignments, deletions}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal canonical delivery snapshot: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validUUID(value string) bool     { return uuidPattern.MatchString(value) }
func validDigest(value string) bool   { return digestPattern.MatchString(value) }
func validDNSLabel(value string) bool { return len(value) <= 63 && dnsLabelPattern.MatchString(value) }
func validSecretKey(value string) bool {
	return len(value) <= 253 && secretKeyPattern.MatchString(value)
}
