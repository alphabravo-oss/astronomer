package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"sigs.k8s.io/yaml"
)

const (
	MaxAssignmentBytes = 16 << 20
	MaxSnapshotBytes   = 64 << 20
	maxCollectionItems = 256
	maxStringBytes     = 64 << 10
)

var (
	substitutionKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	dependencyNamePattern  = regexp.MustCompile(`^d-[0-9a-f]{32}$`)
)

// Capabilities is the fail-closed contract between controller bootstrap and
// workload materialization. A zero value deliberately permits nothing.
type Capabilities struct {
	SourceKinds            []protocol.DeliverySourceKind
	RendererKinds          []protocol.DeliveryRendererKind
	FluxAPIVersions        []string
	NamespaceScope         bool
	PlatformScope          bool
	NoCrossNamespaceRefs   bool
	NoRemoteKustomizeBases bool
}

// ValidationPolicy holds enrollment decisions that are not expressed in an
// assignment. Platform scope requires both the cluster capability and this
// explicit release-time authorization.
type ValidationPolicy struct {
	AllowPlatformScope bool
}

// ValidateSnapshot validates the complete snapshot and all local capability
// constraints before the caller performs any Kubernetes write.
func ValidateSnapshot(snapshot protocol.DeliveryStateResponseV2, capabilities Capabilities, policy ValidationPolicy) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("protocol snapshot: %w", err)
	}
	if snapshot.NotModified {
		return nil
	}
	if !capabilities.NoCrossNamespaceRefs || !capabilities.NoRemoteKustomizeBases {
		return errors.New("delivery controllers lack required cross-namespace or remote-base isolation")
	}

	total := 0
	for i := range snapshot.Assignments {
		encoded, err := json.Marshal(snapshot.Assignments[i])
		if err != nil {
			return fmt.Errorf("assignment %d encoding: %w", i, err)
		}
		if len(encoded) > MaxAssignmentBytes {
			return fmt.Errorf("assignment %d exceeds %d-byte materialization limit", i, MaxAssignmentBytes)
		}
		total += len(encoded)
		if total > MaxSnapshotBytes {
			return fmt.Errorf("snapshot exceeds %d-byte materialization limit", MaxSnapshotBytes)
		}
		if err := ValidateAssignment(snapshot.Assignments[i], capabilities, policy); err != nil {
			return fmt.Errorf("assignment %d: %w", i, err)
		}
	}
	return nil
}

func ValidateAssignment(assignment protocol.DeliveryAssignmentV2, capabilities Capabilities, policy ValidationPolicy) error {
	if err := assignment.Validate(); err != nil {
		return fmt.Errorf("protocol assignment: %w", err)
	}
	if !containsSource(capabilities.SourceKinds, assignment.Source.Kind) {
		return fmt.Errorf("source kind %q is not advertised by this agent", assignment.Source.Kind)
	}
	if !containsRenderer(capabilities.RendererKinds, assignment.Renderer.Kind) {
		return fmt.Errorf("renderer %q is not advertised by this agent", assignment.Renderer.Kind)
	}
	if assignment.Scope == protocol.DeliveryScopeNamespace && !capabilities.NamespaceScope {
		return errors.New("namespace-scoped delivery is not advertised by this agent")
	}
	if assignment.Scope == protocol.DeliveryScopePlatform && (!capabilities.PlatformScope || !policy.AllowPlatformScope) {
		return errors.New("platform-scoped delivery lacks capability or explicit authorization")
	}

	names := Names(assignment.ProjectID, assignment.DeploymentID)
	targetNamespace, serviceAccount := rendererBoundary(assignment.Renderer)
	if targetNamespace == DeliverySystemNamespace || strings.HasPrefix(targetNamespace, ProjectNamespacePrefix) {
		return fmt.Errorf("target namespace %q is reserved for delivery control resources", targetNamespace)
	}
	wantServiceAccount := names.Applier
	if serviceAccount != wantServiceAccount {
		return fmt.Errorf("renderer service account must be deterministic %q", wantServiceAccount)
	}

	if err := validateSourceRendererPair(assignment); err != nil {
		return err
	}
	if err := validateSourcePolicy(assignment, names); err != nil {
		return err
	}
	if err := validateRendererDetails(assignment, targetNamespace); err != nil {
		return err
	}
	for _, apiVersion := range requiredFluxAPIVersions(assignment) {
		if !containsString(capabilities.FluxAPIVersions, apiVersion) {
			return fmt.Errorf("required Flux API %q is not advertised by this agent", apiVersion)
		}
	}
	return nil
}

func validateSourceRendererPair(assignment protocol.DeliveryAssignmentV2) error {
	source, renderer := assignment.Source, assignment.Renderer
	switch renderer.Kind {
	case protocol.DeliveryRendererKustomize:
		if source.Kind != protocol.DeliverySourceGit && source.Kind != protocol.DeliverySourceOCIArtifact {
			return fmt.Errorf("kustomize renderer cannot consume source kind %q", source.Kind)
		}
		if source.Chart != "" {
			return errors.New("kustomize source cannot declare a Helm chart")
		}
	case protocol.DeliveryRendererHelm:
		if source.Kind != protocol.DeliverySourceHelmHTTP && source.Kind != protocol.DeliverySourceHelmOCI {
			return fmt.Errorf("helm renderer cannot consume source kind %q", source.Kind)
		}
		if source.Path != "" {
			return errors.New("helm source cannot declare a kustomize path")
		}
		if source.Chart != renderer.Helm.Chart || source.Revision != renderer.Helm.Version {
			return errors.New("helm renderer chart and version must equal the immutable resolved source")
		}
	}
	return nil
}

func validateSourcePolicy(assignment protocol.DeliveryAssignmentV2, names ObjectNames) error {
	source := assignment.Source
	allowedProviders := map[protocol.DeliverySourceKind]map[string]bool{
		protocol.DeliverySourceGit:         {"": true, "generic": true, "azure": true, "github": true},
		protocol.DeliverySourceOCIArtifact: {"": true, "generic": true, "aws": true, "azure": true, "gcp": true},
		protocol.DeliverySourceHelmHTTP:    {"": true, "generic": true},
		protocol.DeliverySourceHelmOCI:     {"": true, "generic": true, "aws": true, "azure": true, "gcp": true},
	}
	if !allowedProviders[source.Kind][source.Provider] {
		return fmt.Errorf("provider %q is not supported for source kind %q", source.Provider, source.Kind)
	}

	authKeys, trustKeys, err := partitionCredential(assignment.Credential)
	if err != nil {
		return err
	}
	if source.CredentialSecret != "" {
		if source.CredentialSecret != names.AuthSecret {
			return fmt.Errorf("credential Secret must be deterministic %q", names.AuthSecret)
		}
		if len(authKeys) == 0 {
			return errors.New("credential Secret reference has no authentication material")
		}
	} else if len(authKeys) != 0 {
		return errors.New("authentication material has no credential Secret reference")
	}
	if err := validateAuthenticationKeys(source, authKeys); err != nil {
		return err
	}

	if source.TrustSecret != "" {
		if source.TrustSecret != names.TrustSecret {
			return fmt.Errorf("trust Secret must be deterministic %q", names.TrustSecret)
		}
		if len(trustKeys) == 0 {
			return errors.New("trust Secret reference has no trust material")
		}
	} else if len(trustKeys) != 0 {
		return errors.New("trust material has no trust Secret reference")
	}
	if assignment.Credential != nil && len(authKeys)+len(trustKeys) == 0 {
		return errors.New("credential contains no recognized Flux Secret keys")
	}

	verify := source.Verify
	if verify == nil {
		if source.TrustSecret != "" {
			return errors.New("trust Secret requires a source verification policy")
		}
		return nil
	}
	if source.Kind == protocol.DeliverySourceGit {
		if verify.Provider != "pgp" || (verify.Mode != "HEAD" && verify.Mode != "Tag" && verify.Mode != "TagAndHEAD") || source.TrustSecret == "" || len(verify.OIDCIdentities) != 0 || len(trustKeys) != 1 || trustKeys["pgp.asc"] == nil {
			return errors.New("git verification requires PGP trust and HEAD, Tag, or TagAndHEAD mode")
		}
		return nil
	}
	if source.Kind != protocol.DeliverySourceOCIArtifact && source.Kind != protocol.DeliverySourceHelmOCI {
		return errors.New("source verification is unsupported for this source kind")
	}
	if verify.Provider != "cosign" && verify.Provider != "notation" {
		return fmt.Errorf("unsupported OCI verification provider %q", verify.Provider)
	}
	if verify.Mode != "" {
		return errors.New("OCI verification does not accept a git verification mode")
	}
	if len(verify.OIDCIdentities) > 16 {
		return errors.New("OCI verification identity count exceeds 16")
	}
	if source.TrustSecret == "" && len(verify.OIDCIdentities) == 0 {
		return errors.New("OCI verification requires trust keys or bounded OIDC identities")
	}
	for _, identity := range verify.OIDCIdentities {
		if identity.Issuer == "" || identity.Subject == "" || len(identity.Issuer) > 2048 || len(identity.Subject) > 2048 {
			return errors.New("OCI verification contains an invalid OIDC identity")
		}
	}
	if verify.Provider == "cosign" {
		if source.TrustSecret != "" && (len(trustKeys) != 1 || trustKeys["cosign.pub"] == nil) {
			return errors.New("cosign key verification requires exactly one cosign public key")
		}
		if source.TrustSecret != "" && len(verify.OIDCIdentities) != 0 {
			return errors.New("cosign key and keyless OIDC verification cannot be combined")
		}
	}
	if verify.Provider == "notation" {
		if len(verify.OIDCIdentities) != 0 || trustKeys["trustpolicy.json"] == nil || len(trustKeys) < 2 {
			return errors.New("notation verification requires a trust policy and certificate material")
		}
		for key := range trustKeys {
			if key != "trustpolicy.json" && !strings.HasSuffix(key, ".crt") && !strings.HasSuffix(key, ".pem") {
				return fmt.Errorf("notation trust key %q is not a policy or certificate", key)
			}
		}
	}
	return nil
}

func validateAuthenticationKeys(source protocol.DeliverySourceV2, data map[string][]byte) error {
	if len(data) == 0 {
		return nil
	}
	parsed, _ := url.Parse(source.URL)
	allowed := map[string]bool{}
	switch source.Kind {
	case protocol.DeliverySourceGit:
		if parsed.Scheme == "ssh" {
			allowed = map[string]bool{"identity": true, "known_hosts": true, "password": true}
			if data["identity"] == nil || data["known_hosts"] == nil {
				return errors.New("authenticated SSH git sources require identity and known_hosts")
			}
		} else {
			allowed = map[string]bool{"username": true, "password": true, "bearerToken": true, "ca.crt": true, "tls.crt": true, "tls.key": true}
			if data["bearerToken"] != nil && (data["username"] != nil || data["password"] != nil) {
				return errors.New("git bearer and basic authentication cannot be combined")
			}
			if (data["tls.crt"] == nil) != (data["tls.key"] == nil) {
				return errors.New("git client TLS certificate and key must be supplied together")
			}
		}
	case protocol.DeliverySourceHelmHTTP:
		allowed = map[string]bool{"username": true, "password": true, "certFile": true, "keyFile": true, "caFile": true}
		if (data["certFile"] == nil) != (data["keyFile"] == nil) {
			return errors.New("Helm client TLS certificate and key must be supplied together")
		}
	case protocol.DeliverySourceOCIArtifact, protocol.DeliverySourceHelmOCI:
		allowed = map[string]bool{"username": true, "password": true, ".dockerconfigjson": true, "certFile": true, "keyFile": true, "caFile": true}
		if data[".dockerconfigjson"] != nil && len(data) != 1 {
			return errors.New("docker config authentication cannot be combined with other OCI keys")
		}
		if (data["certFile"] == nil) != (data["keyFile"] == nil) {
			return errors.New("OCI client TLS certificate and key must be supplied together")
		}
	}
	for key := range data {
		if !allowed[key] {
			return fmt.Errorf("authentication key %q is unsupported for source kind %q", key, source.Kind)
		}
	}
	return nil
}

func validateRendererDetails(assignment protocol.DeliveryAssignmentV2, targetNamespace string) error {
	switch assignment.Renderer.Kind {
	case protocol.DeliveryRendererKustomize:
		config := assignment.Renderer.Kustomize
		if config.Prune != assignment.Policy.Prune {
			return errors.New("kustomize prune setting must equal the released reconciliation policy")
		}
		if len(config.Patches) > maxCollectionItems || len(config.Substitutions) > maxCollectionItems || len(config.HealthChecks) > maxCollectionItems || len(config.DependencyNames) > maxCollectionItems {
			return errors.New("kustomize renderer collection exceeds 256 entries")
		}
		for index, patch := range config.Patches {
			if strings.TrimSpace(patch) == "" {
				return fmt.Errorf("kustomize patch %d is empty", index)
			}
			if _, err := yaml.YAMLToJSON([]byte(patch)); err != nil {
				return fmt.Errorf("kustomize patch %d is not one valid YAML/JSON document: %w", index, err)
			}
		}
		for key, value := range config.Substitutions {
			if !substitutionKeyPattern.MatchString(key) || len(value) > maxStringBytes {
				return fmt.Errorf("kustomize substitution %q is invalid", key)
			}
		}
		for _, health := range config.HealthChecks {
			if health.APIVersion == "" || health.Kind == "" || health.Name == "" || len(health.APIVersion)+len(health.Kind)+len(health.Name) > 768 {
				return errors.New("kustomize health check is invalid")
			}
			if assignment.Scope == protocol.DeliveryScopeNamespace && health.Namespace != "" && health.Namespace != targetNamespace {
				return errors.New("namespace-scoped health check escapes the target namespace")
			}
		}
		if err := validateDependencies(config.DependencyNames); err != nil {
			return err
		}
	case protocol.DeliveryRendererHelm:
		config := assignment.Renderer.Helm
		if strings.Contains(config.Chart, "..") || strings.HasPrefix(config.Chart, "/") || len(config.Chart) > 255 ||
			strings.Contains(assignment.Source.Chart, "..") || strings.HasPrefix(assignment.Source.Chart, "/") {
			return errors.New("helm chart name is invalid")
		}
		if config.InstallRetries < 0 || config.InstallRetries > 30 || config.UpgradeRetries < 0 || config.UpgradeRetries > 30 {
			return errors.New("helm remediation retries must be between zero and 30")
		}
		if config.UpgradeRemediation != "rollback" && config.UpgradeRemediation != "uninstall" {
			return errors.New("helm upgrade remediation must be rollback or uninstall")
		}
		if config.DriftMode != "enabled" && config.DriftMode != "warn" && config.DriftMode != "disabled" {
			return errors.New("helm drift mode must be enabled, warn, or disabled")
		}
		if len(config.DependencyNames) > maxCollectionItems {
			return errors.New("helm dependency collection exceeds 256 entries")
		}
		if err := validateDependencies(config.DependencyNames); err != nil {
			return err
		}
	}
	for label, value := range map[string]string{"interval": assignment.Policy.Interval, "timeout": assignment.Policy.Timeout, "retry interval": assignment.Policy.RetryInterval} {
		if value == "" && label == "retry interval" {
			continue
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration < 5*time.Second || duration > 24*time.Hour {
			return fmt.Errorf("%s must be between 5s and 24h", label)
		}
	}
	return nil
}

func validateDependencies(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !dependencyNamePattern.MatchString(name) {
			return fmt.Errorf("dependency %q is not a deterministic workload deployment name", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate dependency %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func rendererBoundary(renderer protocol.DeliveryRendererV2) (string, string) {
	if renderer.Kustomize != nil {
		return renderer.Kustomize.TargetNamespace, renderer.Kustomize.ServiceAccount
	}
	if renderer.Helm != nil {
		return renderer.Helm.TargetNamespace, renderer.Helm.ServiceAccount
	}
	return "", ""
}

func requiredFluxAPIVersions(assignment protocol.DeliveryAssignmentV2) []string {
	versions := []string{"source.toolkit.fluxcd.io/v1"}
	if assignment.Renderer.Kind == protocol.DeliveryRendererKustomize {
		versions = append(versions, "kustomize.toolkit.fluxcd.io/v1")
	} else {
		versions = append(versions, "helm.toolkit.fluxcd.io/v2")
	}
	return versions
}

func partitionCredential(credential *protocol.DeliveryCredentialMaterial) (map[string][]byte, map[string][]byte, error) {
	auth, trust := make(map[string][]byte), make(map[string][]byte)
	if credential == nil {
		return auth, trust, nil
	}
	keyMap := map[string]string{
		"identity":                   "identity",
		"known_hosts":                "known_hosts",
		"username":                   "username",
		"password":                   "password",
		"bearerToken":                "bearerToken",
		"ca.crt":                     "ca.crt",
		"tls.crt":                    "tls.crt",
		"tls.key":                    "tls.key",
		"certFile":                   "certFile",
		"keyFile":                    "keyFile",
		"caFile":                     "caFile",
		"dockerconfigjson":           ".dockerconfigjson",
		"trust.cosign.pub":           "cosign.pub",
		"trust.pgp.asc":              "pgp.asc",
		"trust.notation.policy.json": "trustpolicy.json",
		"trust.notation.ca.pem":      "ca.pem",
	}
	keys := make([]string, 0, len(credential.Data))
	for key := range credential.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		secretKey, ok := keyMap[key]
		if !ok {
			return nil, nil, fmt.Errorf("credential key %q is not an allowlisted Flux Secret key", key)
		}
		value := append([]byte(nil), credential.Data[key]...)
		if strings.HasPrefix(key, "trust.") {
			trust[secretKey] = value
		} else {
			auth[secretKey] = value
		}
	}
	return auth, trust, nil
}

func containsSource(values []protocol.DeliverySourceKind, want protocol.DeliverySourceKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsRenderer(values []protocol.DeliveryRendererKind, want protocol.DeliveryRendererKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
