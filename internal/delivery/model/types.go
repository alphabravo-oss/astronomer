package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	MaxNameLength              = 253
	MaxDescriptionLength       = 4096
	MaxURLLength               = 4096
	MaxRequestedRevisionLength = 1024
	MaxValuesBytes             = 1 << 20
	MaxPatchBytes              = 256 << 10
	MaxCapabilities            = 128
)

var (
	gitCommitPattern = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)
	dnsLabelPattern  = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
)

type SourceType string

const (
	SourceGit         SourceType = "git"
	SourceOCIArtifact SourceType = "oci_artifact"
	SourceHelmHTTP    SourceType = "helm_http"
	SourceHelmOCI     SourceType = "helm_oci"
)

type AuthMode string

const (
	AuthNone             AuthMode = "none"
	AuthBasic            AuthMode = "basic"
	AuthBearer           AuthMode = "bearer"
	AuthSSH              AuthMode = "ssh"
	AuthWorkloadIdentity AuthMode = "workload_identity"
)

type SignatureProvider string

const (
	SignatureCosignKey     SignatureProvider = "cosign_key"
	SignatureCosignKeyless SignatureProvider = "cosign_keyless"
	SignatureGit           SignatureProvider = "git"
)

// TrustPolicy contains references and public identity constraints only. Secret
// key material belongs in the credential store and must never enter this type.
type TrustPolicy struct {
	AllowUnsigned bool              `json:"allow_unsigned"`
	Provider      SignatureProvider `json:"provider,omitempty"`
	Identity      string            `json:"identity,omitempty"`
	Issuer        string            `json:"issuer,omitempty"`
	KeyRef        string            `json:"key_ref,omitempty"`
}

func (p TrustPolicy) Validate() error {
	var collector validationCollector
	if len(p.Identity) > 2048 || containsControl(p.Identity) {
		collector.add("identity", CodeInvalid, "must be a bounded public identity without control characters")
	}
	if len(p.Issuer) > 2048 || containsControl(p.Issuer) {
		collector.add("issuer", CodeInvalid, "must be a bounded public issuer without control characters")
	}
	if p.KeyRef != "" {
		if errs := utilvalidation.IsDNS1123Subdomain(p.KeyRef); len(errs) != 0 {
			collector.add("key_ref", CodeInvalid, "must be a DNS subdomain reference, not inline key material")
		}
	}
	if p.AllowUnsigned {
		if p.Provider != "" || p.Identity != "" || p.Issuer != "" || p.KeyRef != "" {
			collector.add("allow_unsigned", CodeConflict, "cannot be combined with signature verification fields")
		}
		return collector.err()
	}
	switch p.Provider {
	case SignatureCosignKey:
		if strings.TrimSpace(p.KeyRef) == "" {
			collector.add("key_ref", CodeRequired, "is required for cosign key verification")
		}
		if p.Identity != "" || p.Issuer != "" {
			collector.add("identity", CodeConflict, "identity and issuer are only valid for keyless verification")
		}
	case SignatureCosignKeyless:
		if strings.TrimSpace(p.Identity) == "" {
			collector.add("identity", CodeRequired, "is required for keyless verification")
		}
		if strings.TrimSpace(p.Issuer) == "" {
			collector.add("issuer", CodeRequired, "is required for keyless verification")
		}
		if p.KeyRef != "" {
			collector.add("key_ref", CodeConflict, "is not valid for keyless verification")
		}
	case SignatureGit:
		if strings.TrimSpace(p.KeyRef) == "" {
			collector.add("key_ref", CodeRequired, "is required for Git verification")
		}
		if p.Issuer != "" {
			collector.add("issuer", CodeConflict, "is not valid for Git verification")
		}
	case "":
		collector.add("provider", CodeRequired, "is required when unsigned artifacts are disallowed")
	default:
		collector.add("provider", CodeUnsupported, "is not a supported signature provider")
	}
	return collector.err()
}

// Source is the non-secret, reusable source definition.
type Source struct {
	ID          uuid.UUID   `json:"id"`
	ProjectID   uuid.UUID   `json:"project_id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Type        SourceType  `json:"type"`
	URL         string      `json:"url"`
	AuthMode    AuthMode    `json:"auth_mode"`
	Trust       TrustPolicy `json:"trust_policy"`
}

func (s Source) Validate() error {
	var collector validationCollector
	validateUUID(&collector, "id", s.ID)
	validateUUID(&collector, "project_id", s.ProjectID)
	validateName(&collector, "name", s.Name)
	if len(s.Description) > MaxDescriptionLength {
		collector.add("description", CodeLimitExceeded, fmt.Sprintf("must be at most %d bytes", MaxDescriptionLength))
	}
	validateSourceConnection(&collector, s.Type, s.URL, s.AuthMode, s.Trust)
	return collector.err()
}

func validateSourceConnection(collector *validationCollector, sourceType SourceType, sourceURL string, authMode AuthMode, trust TrustPolicy) {
	switch sourceType {
	case SourceGit, SourceOCIArtifact, SourceHelmHTTP, SourceHelmOCI:
	default:
		collector.add("type", CodeUnsupported, "is not a supported delivery source type")
	}
	parsed := validateSourceURL(collector, sourceType, sourceURL)
	if !trust.AllowUnsigned {
		switch sourceType {
		case SourceGit:
			if trust.Provider != SignatureGit {
				collector.add("trust_policy.provider", CodeConflict, "Git sources require the git signature provider")
			}
		case SourceOCIArtifact, SourceHelmHTTP, SourceHelmOCI:
			if trust.Provider != SignatureCosignKey && trust.Provider != SignatureCosignKeyless {
				collector.add("trust_policy.provider", CodeConflict, "artifact sources require a cosign signature provider")
			}
		}
	}
	switch authMode {
	case AuthNone, AuthBasic, AuthBearer, AuthWorkloadIdentity:
	case AuthSSH:
		if sourceType != SourceGit {
			collector.add("auth_mode", CodeConflict, "SSH authentication is only valid for Git sources")
		}
		if parsed != nil && parsed.Scheme != "ssh" {
			collector.add("auth_mode", CodeConflict, "SSH authentication requires an ssh source URL")
		}
	default:
		collector.add("auth_mode", CodeUnsupported, "is not a supported authentication mode")
	}
	collector.append("trust_policy", trust.Validate())
}

func validateSourceURL(collector *validationCollector, sourceType SourceType, value string) *url.URL {
	if value == "" {
		collector.add("url", CodeRequired, "is required")
		return nil
	}
	if len(value) > MaxURLLength {
		collector.add("url", CodeLimitExceeded, fmt.Sprintf("must be at most %d bytes", MaxURLLength))
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.RawQuery != "" {
		collector.add("url", CodeInvalid, "must be an absolute URL without a query or fragment")
		return nil
	}
	if parsed.User != nil {
		_, hasPassword := parsed.User.Password()
		if parsed.Scheme != "ssh" || parsed.User.Username() == "" || hasPassword {
			collector.add("url", CodeSecretNotAllowed, "must not contain embedded credentials")
		}
	}
	allowed := false
	switch sourceType {
	case SourceGit:
		allowed = parsed.Scheme == "https" || parsed.Scheme == "ssh"
	case SourceOCIArtifact, SourceHelmOCI:
		allowed = parsed.Scheme == "oci"
	case SourceHelmHTTP:
		allowed = parsed.Scheme == "https"
	}
	if !allowed {
		collector.add("url", CodeInvalid, "scheme is not allowed for the selected source type")
	}
	return parsed
}

type RevisionKind string

const (
	RevisionGitCommit RevisionKind = "git_commit"
	RevisionOCIDigest RevisionKind = "oci_digest"
	RevisionHelmChart RevisionKind = "helm_chart"
)

// ImmutableRevision is the centrally resolved identity Flux must reconcile.
// ArtifactDigest is always required, including Git and HTTP Helm sources.
type ImmutableRevision struct {
	Kind           RevisionKind `json:"kind"`
	Value          string       `json:"value"`
	ArtifactDigest Digest       `json:"artifact_digest"`
}

// ResolvedSourceSpec is the immutable, non-secret source projection placed in
// an assignment. Credential material is transported separately and is never a
// field of this structure.
type ResolvedSourceSpec struct {
	SourceID uuid.UUID         `json:"source_id"`
	Type     SourceType        `json:"type"`
	URL      string            `json:"url"`
	AuthMode AuthMode          `json:"auth_mode"`
	Trust    TrustPolicy       `json:"trust_policy"`
	Revision ImmutableRevision `json:"revision"`
}

func (s ResolvedSourceSpec) Validate() error {
	var collector validationCollector
	validateUUID(&collector, "source_id", s.SourceID)
	validateSourceConnection(&collector, s.Type, s.URL, s.AuthMode, s.Trust)
	collector.append("revision", s.Revision.Validate())
	return collector.err()
}

func (r ImmutableRevision) Validate() error {
	var collector validationCollector
	switch r.Kind {
	case RevisionGitCommit:
		if !gitCommitPattern.MatchString(r.Value) {
			collector.add("value", CodeNotImmutable, "must be a full lowercase 40- or 64-character Git commit")
		}
	case RevisionOCIDigest:
		if digest, err := ParseDigest(r.Value); err != nil || digest == "" {
			collector.add("value", CodeNotImmutable, "must be an immutable sha256 OCI digest")
		}
	case RevisionHelmChart:
		if strings.TrimSpace(r.Value) == "" || len(r.Value) > MaxRequestedRevisionLength {
			collector.add("value", CodeNotImmutable, "must be an exact bounded chart version")
		} else if _, err := semver.NewVersion(r.Value); err != nil {
			collector.add("value", CodeNotImmutable, "must be an exact semantic chart version")
		}
	default:
		collector.add("kind", CodeUnsupported, "is not a supported immutable revision kind")
	}
	if err := r.ArtifactDigest.Validate(); err != nil {
		collector.add("artifact_digest", CodeInvalid, "must be a canonical SHA-256 digest")
	}
	return collector.err()
}

// CapabilityRequirement is a negotiated feature and optional semantic-version
// constraint. A blank constraint means the capability bit alone is sufficient.
type CapabilityRequirement struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint,omitempty"`
}

func (r CapabilityRequirement) Validate() error {
	var collector validationCollector
	if errs := utilvalidation.IsQualifiedName(r.Name); len(errs) != 0 {
		collector.add("name", CodeInvalid, "must be a Kubernetes qualified name")
	}
	if len(r.Constraint) > 128 {
		collector.add("constraint", CodeLimitExceeded, "must be at most 128 bytes")
	} else if r.Constraint != "" {
		if _, err := semver.NewConstraint(r.Constraint); err != nil {
			collector.add("constraint", CodeInvalid, "must be a valid semantic-version constraint")
		}
	}
	return collector.err()
}

// CapabilityRequirementsCanonical validates, de-duplicates, and sorts a set.
func CapabilityRequirementsCanonical(input []CapabilityRequirement) ([]CapabilityRequirement, error) {
	if len(input) > MaxCapabilities {
		return nil, &ValidationError{Violations: []Violation{{Field: "capabilities", Code: CodeLimitExceeded, Message: fmt.Sprintf("must contain at most %d entries", MaxCapabilities)}}}
	}
	result := append([]CapabilityRequirement(nil), input...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Constraint < result[j].Constraint
		}
		return result[i].Name < result[j].Name
	})
	var collector validationCollector
	for i, requirement := range result {
		collector.append(fmt.Sprintf("capabilities[%d]", i), requirement.Validate())
		if i > 0 && result[i-1].Name == requirement.Name {
			collector.add(fmt.Sprintf("capabilities[%d].name", i), CodeConflict, "capability names must be unique")
		}
	}
	return result, collector.err()
}

type RendererKind string

const (
	RendererKustomize RendererKind = "kustomize"
	RendererHelm      RendererKind = "helm"
)

type Scope string

const (
	ScopeNamespace Scope = "namespace"
	ScopePlatform  Scope = "platform"
)

type KustomizeSpec struct {
	Path            string   `json:"path"`
	TargetNamespace string   `json:"target_namespace"`
	Patches         []string `json:"patches,omitempty"`
}

type HelmSpec struct {
	Chart           string          `json:"chart"`
	ChartVersion    string          `json:"chart_version"`
	ReleaseName     string          `json:"release_name"`
	TargetNamespace string          `json:"target_namespace"`
	Values          json.RawMessage `json:"values,omitempty"`
	InstallRetries  uint8           `json:"install_retries"`
	UpgradeRetries  uint8           `json:"upgrade_retries"`
	Test            bool            `json:"test"`
}

// RendererSpec is a closed discriminated union. Exactly one variant is
// required and it must match Kind.
type RendererSpec struct {
	Kind      RendererKind   `json:"kind"`
	Kustomize *KustomizeSpec `json:"kustomize,omitempty"`
	Helm      *HelmSpec      `json:"helm,omitempty"`
}

func (s RendererSpec) Validate() error {
	var collector validationCollector
	switch s.Kind {
	case RendererKustomize:
		if s.Kustomize == nil {
			collector.add("kustomize", CodeRequired, "is required for the kustomize renderer")
		} else {
			collector.append("kustomize", s.Kustomize.validate())
		}
		if s.Helm != nil {
			collector.add("helm", CodeConflict, "must be absent for the kustomize renderer")
		}
	case RendererHelm:
		if s.Helm == nil {
			collector.add("helm", CodeRequired, "is required for the helm renderer")
		} else {
			collector.append("helm", s.Helm.validate())
		}
		if s.Kustomize != nil {
			collector.add("kustomize", CodeConflict, "must be absent for the helm renderer")
		}
	default:
		collector.add("kind", CodeUnsupported, "must be kustomize or helm")
	}
	return collector.err()
}

type DriftPolicy string

const (
	DriftIgnore DriftPolicy = "ignore"
	DriftDetect DriftPolicy = "detect"
	DriftRepair DriftPolicy = "repair"
)

// ReconciliationPolicy contains behavior shared by both Flux renderer kinds.
// Renderer-specific install/upgrade choices remain in RendererSpec.
type ReconciliationPolicy struct {
	Interval      Duration    `json:"interval"`
	RetryInterval Duration    `json:"retry_interval"`
	Timeout       Duration    `json:"timeout"`
	Prune         bool        `json:"prune"`
	Wait          bool        `json:"wait"`
	Drift         DriftPolicy `json:"drift"`
}

func (p ReconciliationPolicy) Validate() error {
	var collector validationCollector
	validatePositiveDuration(&collector, "interval", p.Interval)
	validatePositiveDuration(&collector, "retry_interval", p.RetryInterval)
	validatePositiveDuration(&collector, "timeout", p.Timeout)
	switch p.Drift {
	case DriftIgnore, DriftDetect, DriftRepair:
	default:
		collector.add("drift", CodeUnsupported, "must be ignore, detect, or repair")
	}
	return collector.err()
}

// Duration has string JSON encoding so public-domain values cannot silently
// change units when passed between API, workers, and approval digests.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration: %w", err)
	}
	*d = Duration(parsed)
	return nil
}

// BundleVersionSpec is exactly the immutable, digest-bound part of a bundle
// version. RequestedRevision records user intent; Revision records what was
// resolved and verified.
type BundleVersionSpec struct {
	SourceID             uuid.UUID               `json:"source_id"`
	RequestedRevision    string                  `json:"requested_revision"`
	Revision             ImmutableRevision       `json:"revision"`
	Renderer             RendererSpec            `json:"renderer"`
	Scope                Scope                   `json:"scope"`
	Reconciliation       ReconciliationPolicy    `json:"reconciliation_policy"`
	RequiredCapabilities []CapabilityRequirement `json:"required_capabilities,omitempty"`
}

// BundleVersionDraft is accepted at the API boundary before the resolver has
// converted RequestedRevision into an immutable, verified Revision. It is never
// released to an agent and is replaced atomically by BundleVersionSpec.
type BundleVersionDraft struct {
	SourceID             uuid.UUID               `json:"source_id"`
	RequestedRevision    string                  `json:"requested_revision"`
	Renderer             RendererSpec            `json:"renderer"`
	Scope                Scope                   `json:"scope"`
	Reconciliation       ReconciliationPolicy    `json:"reconciliation_policy"`
	RequiredCapabilities []CapabilityRequirement `json:"required_capabilities,omitempty"`
}

func (s BundleVersionDraft) Validate() error {
	var collector validationCollector
	validateUUID(&collector, "source_id", s.SourceID)
	if strings.TrimSpace(s.RequestedRevision) == "" {
		collector.add("requested_revision", CodeRequired, "is required")
	} else if len(s.RequestedRevision) > MaxRequestedRevisionLength {
		collector.add("requested_revision", CodeLimitExceeded, fmt.Sprintf("must be at most %d bytes", MaxRequestedRevisionLength))
	}
	switch s.Scope {
	case ScopeNamespace, ScopePlatform:
	default:
		collector.add("scope", CodeUnsupported, "must be namespace or platform")
	}
	collector.append("renderer", s.Renderer.Validate())
	collector.append("reconciliation_policy", s.Reconciliation.Validate())
	_, err := CapabilityRequirementsCanonical(s.RequiredCapabilities)
	collector.append("required_capabilities", err)
	return collector.err()
}

func (s BundleVersionDraft) Resolve(revision ImmutableRevision) (BundleVersionSpec, error) {
	if err := s.Validate(); err != nil {
		return BundleVersionSpec{}, err
	}
	resolved := BundleVersionSpec{
		SourceID: s.SourceID, RequestedRevision: s.RequestedRevision,
		Revision: revision, Renderer: s.Renderer, Scope: s.Scope,
		Reconciliation: s.Reconciliation, RequiredCapabilities: s.RequiredCapabilities,
	}
	if err := resolved.Validate(); err != nil {
		return BundleVersionSpec{}, err
	}
	return resolved, nil
}

func (s BundleVersionSpec) Validate() error {
	var collector validationCollector
	validateUUID(&collector, "source_id", s.SourceID)
	if strings.TrimSpace(s.RequestedRevision) == "" {
		collector.add("requested_revision", CodeRequired, "is required")
	} else if len(s.RequestedRevision) > MaxRequestedRevisionLength {
		collector.add("requested_revision", CodeLimitExceeded, fmt.Sprintf("must be at most %d bytes", MaxRequestedRevisionLength))
	}
	collector.append("revision", s.Revision.Validate())
	switch s.Scope {
	case ScopeNamespace, ScopePlatform:
	default:
		collector.add("scope", CodeUnsupported, "must be namespace or platform")
	}
	collector.append("renderer", s.Renderer.Validate())
	collector.append("reconciliation_policy", s.Reconciliation.Validate())
	_, err := CapabilityRequirementsCanonical(s.RequiredCapabilities)
	collector.append("required_capabilities", err)
	return collector.err()
}

func (s BundleVersionSpec) CanonicalDigest() (Digest, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	canonical := s
	capabilities, _ := CapabilityRequirementsCanonical(s.RequiredCapabilities)
	canonical.RequiredCapabilities = capabilities
	if canonical.Renderer.Kustomize != nil {
		copySpec := *canonical.Renderer.Kustomize
		copySpec.Patches = append([]string(nil), copySpec.Patches...)
		canonical.Renderer.Kustomize = &copySpec
	}
	return CanonicalDigest(canonical)
}

type BundleVersion struct {
	ID         uuid.UUID         `json:"id"`
	BundleID   uuid.UUID         `json:"bundle_id"`
	Version    uint64            `json:"version"`
	Spec       BundleVersionSpec `json:"spec"`
	SpecDigest Digest            `json:"spec_digest"`
	CreatedAt  time.Time         `json:"created_at"`
}

func (v BundleVersion) Validate() error {
	var collector validationCollector
	validateUUID(&collector, "id", v.ID)
	validateUUID(&collector, "bundle_id", v.BundleID)
	if v.Version == 0 {
		collector.add("version", CodeInvalid, "must be greater than zero")
	}
	collector.append("spec", v.Spec.Validate())
	if v.CreatedAt.IsZero() {
		collector.add("created_at", CodeRequired, "is required")
	}
	if digest, err := v.Spec.CanonicalDigest(); err == nil {
		if v.SpecDigest != digest {
			collector.add("spec_digest", CodeInvalid, "does not match the canonical bundle version spec")
		}
	}
	return collector.err()
}

func (s KustomizeSpec) validate() error {
	var collector validationCollector
	clean := path.Clean(s.Path)
	if s.Path == "" || !strings.HasPrefix(s.Path, "./") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		collector.add("path", CodeInvalid, "must be a relative ./ path without parent traversal")
	}
	validateDNSLabel(&collector, "target_namespace", s.TargetNamespace)
	total := 0
	for _, patch := range s.Patches {
		total += len(patch)
	}
	if total > MaxPatchBytes {
		collector.add("patches", CodeLimitExceeded, fmt.Sprintf("must total at most %d bytes", MaxPatchBytes))
	}
	return collector.err()
}

func (s HelmSpec) validate() error {
	var collector validationCollector
	if strings.TrimSpace(s.Chart) == "" || len(s.Chart) > MaxNameLength {
		collector.add("chart", CodeInvalid, "must be a non-empty bounded chart name")
	}
	if _, err := semver.NewVersion(s.ChartVersion); err != nil {
		collector.add("chart_version", CodeNotImmutable, "must be an exact semantic version")
	}
	validateDNSLabel(&collector, "release_name", s.ReleaseName)
	validateDNSLabel(&collector, "target_namespace", s.TargetNamespace)
	if len(s.Values) > MaxValuesBytes {
		collector.add("values", CodeLimitExceeded, fmt.Sprintf("must be at most %d bytes", MaxValuesBytes))
	} else if len(s.Values) != 0 {
		var value any
		if err := json.Unmarshal(s.Values, &value); err != nil {
			collector.add("values", CodeInvalid, "must be valid JSON")
		}
	}
	return collector.err()
}

func validateUUID(collector *validationCollector, field string, value uuid.UUID) {
	if value == uuid.Nil {
		collector.add(field, CodeRequired, "must be a non-zero UUID")
	}
}

func validateName(collector *validationCollector, field, value string) {
	if value == "" {
		collector.add(field, CodeRequired, "is required")
	} else if value != strings.TrimSpace(value) || containsControl(value) {
		collector.add(field, CodeInvalid, "must not contain surrounding whitespace or control characters")
	} else if len(value) > MaxNameLength {
		collector.add(field, CodeLimitExceeded, fmt.Sprintf("must be at most %d bytes", MaxNameLength))
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func validateDNSLabel(collector *validationCollector, field, value string) {
	if value == "" || len(value) > 63 || !dnsLabelPattern.MatchString(value) {
		collector.add(field, CodeInvalid, "must be a lowercase DNS label")
	}
}

func validatePositiveDuration(collector *validationCollector, field string, value Duration) {
	if value <= 0 {
		collector.add(field, CodeInvalid, "must be greater than zero")
	}
}
