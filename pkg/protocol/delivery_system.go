package protocol

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxDeliverySystemTrustBytes = 256 << 10

var (
	semanticVersionPattern   = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	kubernetesVersionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)
	immutableImagePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
)

// DeliverySystemReleaseV2 is the independently fenced desired state for the
// downstream delivery substrate. Workload assignments cannot target the
// system namespace; only this closed schema may change Flux or the agent.
// ArtifactDigest and AgentImage are immutable identities. Generation increases
// for both upgrades and rollbacks, so a rollback never looks like stale state.
type DeliverySystemReleaseV2 struct {
	Generation             int64                       `json:"generation"`
	Version                string                      `json:"version"`
	ArtifactURL            string                      `json:"artifact_url"`
	ArtifactDigest         string                      `json:"artifact_digest"`
	DistributionDigest     string                      `json:"distribution_digest"`
	AgentVersion           string                      `json:"agent_version"`
	AgentImage             string                      `json:"agent_image"`
	MinimumKubernetes      string                      `json:"minimum_kubernetes"`
	MaximumKubernetes      string                      `json:"maximum_kubernetes"`
	CRDStorageVersion      string                      `json:"crd_storage_version"`
	PreviousStorageVersion string                      `json:"previous_storage_version,omitempty"`
	Interval               string                      `json:"interval"`
	Timeout                string                      `json:"timeout"`
	Suspend                bool                        `json:"suspend,omitempty"`
	Verification           DeliverySystemVerification  `json:"verification"`
	Credential             *DeliveryCredentialMaterial `json:"credential,omitempty"`
}

// DeliverySystemVerification is always present. Keyless verification is bound
// to exact OIDC identities. Disconnected installations use an embedded public
// key whose SHA-256 fingerprint is allowlisted by the agent release policy.
type DeliverySystemVerification struct {
	Provider       string                 `json:"provider"`
	OIDCIdentities []DeliveryOIDCIdentity `json:"oidc_identities,omitempty"`
	PublicKey      []byte                 `json:"public_key,omitempty"`
	KeyFingerprint string                 `json:"key_fingerprint,omitempty"`
}

func (r DeliverySystemReleaseV2) Validate() error {
	if r.Generation < 1 || !semanticVersionPattern.MatchString(r.Version) ||
		!validDigest(r.ArtifactDigest) || !validDigest(r.DistributionDigest) {
		return errors.New("generation, semantic version, and immutable digests are required")
	}
	parsed, err := url.Parse(r.ArtifactURL)
	if err != nil || parsed.Scheme != "oci" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("artifact URL must be a credential-free oci:// URL without query or fragment")
	}
	if !semanticVersionPattern.MatchString(r.AgentVersion) || !immutableImagePattern.MatchString(r.AgentImage) {
		return errors.New("agent version and digest-pinned image are required")
	}
	if !kubernetesVersionPattern.MatchString(r.MinimumKubernetes) || !kubernetesVersionPattern.MatchString(r.MaximumKubernetes) {
		return errors.New("bounded Kubernetes compatibility versions are required")
	}
	if !validDNSLabel(r.CRDStorageVersion) || (r.PreviousStorageVersion != "" && !validDNSLabel(r.PreviousStorageVersion)) {
		return errors.New("CRD storage versions must be DNS labels")
	}
	for field, value := range map[string]string{"interval": r.Interval, "timeout": r.Timeout} {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("system release %s must be a positive duration", field)
		}
	}
	if err := r.Verification.Validate(); err != nil {
		return err
	}
	if r.Credential != nil {
		if r.Credential.Version < 1 || len(r.Credential.Data) != 1 {
			return errors.New("system registry credential must contain exactly one versioned value")
		}
		value, ok := r.Credential.Data[".dockerconfigjson"]
		if !ok || len(value) == 0 || len(value) > MaxDeliveryCredentialValue {
			return errors.New("system registry credential must be bounded docker configuration")
		}
	}
	return nil
}

func (v DeliverySystemVerification) Validate() error {
	if v.Provider != "cosign" {
		return fmt.Errorf("system release verification provider %q is not supported", v.Provider)
	}
	if len(v.OIDCIdentities) > 16 || len(v.PublicKey) > MaxDeliverySystemTrustBytes {
		return errors.New("system release verification policy exceeds limits")
	}
	keyMode := len(v.PublicKey) != 0 || v.KeyFingerprint != ""
	identityMode := len(v.OIDCIdentities) != 0
	if keyMode == identityMode {
		return errors.New("system release verification requires exactly one of OIDC identity or public-key mode")
	}
	if keyMode {
		if len(v.PublicKey) == 0 || !validDigest(v.KeyFingerprint) {
			return errors.New("system public-key verification requires key bytes and a sha256 fingerprint")
		}
		return nil
	}
	for index, identity := range v.OIDCIdentities {
		issuer, err := url.Parse(identity.Issuer)
		if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" ||
			strings.TrimSpace(identity.Subject) == "" || len(identity.Subject) > 512 || strings.ContainsAny(identity.Subject, "\r\n\x00") {
			return fmt.Errorf("system OIDC identity %d is invalid", index)
		}
	}
	return nil
}
