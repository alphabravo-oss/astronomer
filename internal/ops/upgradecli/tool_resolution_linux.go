//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"time"

	canonical "github.com/alphabravocompany/astronomer-go/internal/ops/upgradecli/toolchain"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

type bootstrapAuthority struct {
	SchemaVersion int `json:"schemaVersion"`
	Policy        struct {
		MaxDiscoveryAgeDays int    `json:"maxDiscoveryAgeDays"`
		DiscoveryCadence    string `json:"discoveryCadence"`
		TriageCadence       string `json:"triageCadence"`
		ProductionDiscovery bool   `json:"productionDiscovery"`
	} `json:"policy"`
	Components []bootstrapComponent `json:"components"`
	Inventory  json.RawMessage      `json:"inventory"`
}

type bootstrapComponent struct {
	ID        string  `json:"id"`
	Selected  *string `json:"selected"`
	Discovery struct {
		Version    string `json:"version"`
		ObservedAt string `json:"observedAt"`
		ReleasedAt string `json:"releasedAt"`
		Stable     bool   `json:"stable"`
		Provenance string `json:"provenance"`
	} `json:"discovery"`
	Artifacts []struct {
		Digest          string            `json:"digest"`
		Reference       string            `json:"reference"`
		Platforms       []string          `json:"platforms"`
		PlatformDigests map[string]string `json:"platformDigests,omitempty"`
		Provenance      string            `json:"provenance"`
		VerifiedAt      string            `json:"verifiedAt"`
	} `json:"artifacts"`
	Executables    []ExecutableArtifact `json:"executableArtifacts,omitempty"`
	Ecosystem      string               `json:"ecosystem"`
	Owner          string               `json:"owner"`
	Upstream       json.RawMessage      `json:"upstream"`
	Platforms      []string             `json:"platforms"`
	Consumers      json.RawMessage      `json:"consumers"`
	Qualification  string               `json:"qualification"`
	Exception      json.RawMessage      `json:"exception,omitempty"`
	SourceBindings []struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
	} `json:"sourceBindings,omitempty"`
}

type resolutionComponent struct {
	ID              string   `json:"id"`
	Selected        *string  `json:"selected"`
	UpstreamStable  string   `json:"upstreamStable"`
	ObservedAt      string   `json:"observedAt"`
	ArtifactDigests []string `json:"artifactDigests"`
}

type bootstrapResolution struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	AuthorityDigest string                `json:"authorityDigest"`
	SourceCommit    string                `json:"sourceCommit"`
	GeneratedAt     string                `json:"generatedAt"`
	ExpiresAt       string                `json:"expiresAt"`
	Components      []resolutionComponent `json:"components"`
}

var bootstrapSourceCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

// verifyResolutionBinding has no trust authority by itself. BootstrapTools calls
// it only after real offline verification using the compiled canonical root and
// one of the canonical resolution publishers; operator JSON is never authority.
func verifyResolutionBinding(raw, authority []byte, sourceCommit string, now time.Time) (bootstrapAuthority, error) {
	var empty bootstrapAuthority
	if len(raw) == 0 || len(raw) > 1<<20 || len(authority) == 0 || len(authority) > 1<<20 || !bootstrapSourceCommit.MatchString(sourceCommit) {
		return empty, errors.New("exact compiled source and bounded resolution required")
	}
	var selected bootstrapAuthority
	var receipt bootstrapResolution
	if jsoncontract.Decode(authority, &selected) != nil || jsoncontract.Decode(raw, &receipt) != nil || selected.SchemaVersion != 1 || receipt.SchemaVersion != 1 || receipt.SourceCommit != sourceCommit {
		return empty, errors.New("signed resolution source or schema differs")
	}
	sum := sha256.Sum256(authority)
	if receipt.AuthorityDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		return empty, errors.New("signed resolution authority differs")
	}
	generated, err1 := time.Parse(time.RFC3339Nano, receipt.GeneratedAt)
	expires, err2 := time.Parse(time.RFC3339Nano, receipt.ExpiresAt)
	if err1 != nil || err2 != nil || generated.After(now) || !expires.After(now) || !expires.After(generated) || selected.Policy.MaxDiscoveryAgeDays < 1 || selected.Policy.MaxDiscoveryAgeDays > 31 || expires.Sub(generated) > time.Duration(selected.Policy.MaxDiscoveryAgeDays)*24*time.Hour {
		return empty, errors.New("signed resolution freshness differs")
	}
	expected := make([]resolutionComponent, 0, len(selected.Components))
	ids := make(map[string]struct{})
	for _, component := range selected.Components {
		if component.ID == "" {
			return empty, errors.New("canonical component identity missing")
		}
		if _, exists := ids[component.ID]; exists {
			return empty, errors.New("canonical component identity duplicate")
		}
		ids[component.ID] = struct{}{}
		binding := resolutionComponent{ID: component.ID, Selected: component.Selected, UpstreamStable: component.Discovery.Version, ObservedAt: component.Discovery.ObservedAt, ArtifactDigests: make([]string, 0)}
		for _, artifact := range component.Artifacts {
			binding.ArtifactDigests = append(binding.ArtifactDigests, artifact.Digest)
		}
		for _, artifact := range component.Executables {
			binding.ArtifactDigests = append(binding.ArtifactDigests, artifact.Digest)
		}
		expected = append(expected, binding)
	}
	if !reflect.DeepEqual(receipt.Components, expected) {
		return empty, errors.New("signed resolution ordered component artifacts differ")
	}
	return selected, nil
}

// verifyBootstrapResolution owns a self-contained cryptographic verification.
// trustDirectory must contain exactly the root bytes compiled with this consumer;
// checking it is separate from signing success and from artifact extraction.
// The two allowed identities match the maintained resolution producer. Release
// tag input selects only an exact normal publisher, never an operator policy.
func verifyBootstrapResolution(ctx context.Context, record, bundle []byte, trustDirectory, sourceCommit, target string) (bootstrapAuthority, error) {
	var empty bootstrapAuthority
	if ctx == nil || !legacyStableTarget.MatchString(target) {
		return empty, errors.New("explicit signed toolchain bootstrap context/target required")
	}
	expectedRoot := canonical.TrustedRootBytes()
	rootHash := sha256.Sum256(expectedRoot)
	rootDigest := "sha256:" + hex.EncodeToString(rootHash[:])
	actual, err := readCapture(trustDirectory, "trusted_root.json", 1<<20, rootDigest)
	if err != nil || !reflect.DeepEqual(actual, expectedRoot) {
		return empty, errors.New("compiled canonical toolchain root differs")
	}
	verifier, err := resolver.NewExecVerifier(trustDirectory)
	if err != nil {
		return empty, err
	}
	identities := []string{"https://github.com/alphabravo-oss/astronomer/.github/workflows/toolchain-resolution.yaml@refs/heads/main", "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + target}
	verified := false
	for _, identity := range identities {
		policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Issuer: metadataIssuer, Identity: identity}
		if _, err := verifier.VerifyOfflineMetadata(ctx, record, bundle, policy); err == nil {
			verified = true
			break
		}
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
	}
	if !verified {
		return empty, errors.New("signed canonical toolchain resolution verification failed")
	}
	after, err := readCapture(trustDirectory, "trusted_root.json", 1<<20, rootDigest)
	if err != nil || !reflect.DeepEqual(after, expectedRoot) {
		return empty, errors.New("compiled canonical toolchain root changed")
	}
	return verifyResolutionBinding(record, canonical.AuthorityBytes(), sourceCommit, time.Now())
}
