package protocol

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxDeliverySystemTrustBytes = 256 << 10
const MaxDeliverySystemPublicKeys = 8

var (
	// System release identities append build metadata to the app version. A
	// SemVer prerelease (for example, local/dev builds) and build metadata may
	// coexist, so keep both optional suffixes independently representable.
	semanticVersionPattern   = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
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
// to exact OIDC identities. Disconnected installations may publish a bounded
// overlapping key set so operators can rotate signers without a one-key
// cutover. The agent pins every key fingerprint during enrollment.
type DeliverySystemVerification struct {
	Provider       string                 `json:"provider"`
	OIDCIdentities []DeliveryOIDCIdentity `json:"oidc_identities,omitempty"`
	PublicKeys     [][]byte               `json:"public_keys,omitempty"`
	// PublicKey and KeyFingerprint are retained for snapshots produced by
	// pre-keyring servers. New releases must use PublicKeys, including for a
	// one-key set, so the keyring contract has one canonical representation.
	PublicKey      []byte `json:"public_key,omitempty"`
	KeyFingerprint string `json:"key_fingerprint,omitempty"`
}

// DeliverySystemKeyFingerprint identifies the exact PEM bytes pinned at
// enrollment. Keep the bytes stable across the server, manifest, and agent:
// harmless-looking PEM reformatting changes the trust anchor.
func DeliverySystemKeyFingerprint(publicKey []byte) string {
	if len(publicKey) == 0 {
		return ""
	}
	sum := sha256.Sum256(publicKey)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DeliverySystemPublicKeySet returns the canonical set represented by either
// the current keyring fields or the legacy single-key fields. It always copies
// bytes so a caller cannot mutate protocol-owned trust material.
func (v DeliverySystemVerification) DeliverySystemPublicKeySet() [][]byte {
	if len(v.PublicKeys) != 0 {
		keys := make([][]byte, len(v.PublicKeys))
		for index := range v.PublicKeys {
			keys[index] = append([]byte(nil), v.PublicKeys[index]...)
		}
		return keys
	}
	if len(v.PublicKey) == 0 {
		return nil
	}
	return [][]byte{append([]byte(nil), v.PublicKey...)}
}

// DeliverySystemKeyFingerprints computes stable exact-PEM pins for a keyring.
func DeliverySystemKeyFingerprints(publicKeys [][]byte) []string {
	fingerprints := make([]string, 0, len(publicKeys))
	for _, publicKey := range publicKeys {
		fingerprints = append(fingerprints, DeliverySystemKeyFingerprint(publicKey))
	}
	return fingerprints
}

// ParseDeliverySystemPublicKeys decodes the chart's JSON-encoded keyring and
// validates it before it can cross the server/agent boundary. An empty value
// means the caller selected keyless OIDC mode.
func ParseDeliverySystemPublicKeys(encoded string) ([][]byte, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return nil, errors.New("system public-key set must be a JSON array of PEM strings")
	}
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > MaxDeliverySystemPublicKeys {
		return nil, fmt.Errorf("system public-key set must contain at most %d keys", MaxDeliverySystemPublicKeys)
	}
	keys := make([][]byte, len(values))
	for index, value := range values {
		keys[index] = []byte(value)
	}
	if err := (DeliverySystemVerification{Provider: "cosign", PublicKeys: keys}).Validate(); err != nil {
		return nil, err
	}
	return keys, nil
}

// ValidateDeliverySystemPublicKey accepts the standard Cosign PEM public-key
// formats and enforces the protocol size bound before trust material is
// persisted or projected into a cluster Secret.
func ValidateDeliverySystemPublicKey(publicKey []byte) error {
	if len(publicKey) == 0 || len(publicKey) > MaxDeliverySystemTrustBytes {
		return errors.New("system release public key is empty or exceeds the size limit")
	}
	block, rest := pem.Decode(publicKey)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("system release public key must be a single PEM-encoded public key")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return nil
	}
	if _, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return nil
	}
	return errors.New("system release public key PEM is not a supported public-key encoding")
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
	if len(v.OIDCIdentities) > 16 || len(v.PublicKey) > MaxDeliverySystemTrustBytes || len(v.PublicKeys) > MaxDeliverySystemPublicKeys {
		return errors.New("system release verification policy exceeds limits")
	}
	keyMode := len(v.PublicKeys) != 0 || len(v.PublicKey) != 0 || v.KeyFingerprint != ""
	identityMode := len(v.OIDCIdentities) != 0
	if keyMode == identityMode {
		return errors.New("system release verification requires exactly one of OIDC identity or public-key mode")
	}
	if keyMode {
		if len(v.PublicKeys) != 0 {
			if len(v.PublicKey) != 0 || v.KeyFingerprint != "" {
				return errors.New("system release must use either the public-key set or legacy single-key fields")
			}
			var totalBytes int
			fingerprints := make(map[string]struct{}, len(v.PublicKeys))
			for _, publicKey := range v.PublicKeys {
				totalBytes += len(publicKey)
				if totalBytes > MaxDeliverySystemTrustBytes {
					return errors.New("system release public-key set exceeds the size limit")
				}
				if err := ValidateDeliverySystemPublicKey(publicKey); err != nil {
					return err
				}
				fingerprint := DeliverySystemKeyFingerprint(publicKey)
				if _, exists := fingerprints[fingerprint]; exists {
					return errors.New("system release public-key set contains a duplicate key")
				}
				fingerprints[fingerprint] = struct{}{}
			}
			return nil
		}
		if err := ValidateDeliverySystemPublicKey(v.PublicKey); err != nil || !validDigest(v.KeyFingerprint) ||
			DeliverySystemKeyFingerprint(v.PublicKey) != v.KeyFingerprint {
			return errors.New("legacy system public-key verification requires key bytes and a sha256 fingerprint")
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
