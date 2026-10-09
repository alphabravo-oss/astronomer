// Package systemrelease bootstraps the immutable, signed release that owns the
// downstream Astronomer agent and Flux distribution.
package systemrelease

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var kubernetesMinorPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

// Config is sourced from the signed release/chart contract. Credentials are
// intentionally absent; private-mirror credentials are rotated separately.
type Config struct {
	Enabled             bool
	Version             string
	ArtifactRepository  string
	ArtifactDigest      string
	DistributionDigest  string
	AgentVersion        string
	AgentImage          string
	MinimumKubernetes   string
	MaximumKubernetes   string
	CertificateIssuer   string
	CertificateIdentity string
	PublicKeys          [][]byte
	// PublicKey is a temporary compatibility input for existing chart values.
	// build always emits the versioned keyring form in the immutable release.
	PublicKey []byte
}

type immutableSpec struct {
	Version                string                              `json:"version"`
	ArtifactURL            string                              `json:"artifact_url"`
	ArtifactDigest         string                              `json:"artifact_digest"`
	DistributionDigest     string                              `json:"distribution_digest"`
	AgentVersion           string                              `json:"agent_version"`
	AgentImage             string                              `json:"agent_image"`
	MinimumKubernetes      string                              `json:"minimum_kubernetes"`
	MaximumKubernetes      string                              `json:"maximum_kubernetes"`
	CRDStorageVersion      string                              `json:"crd_storage_version"`
	PreviousStorageVersion string                              `json:"previous_storage_version"`
	Interval               string                              `json:"interval"`
	Timeout                string                              `json:"timeout"`
	Verification           protocol.DeliverySystemVerification `json:"verification"`
}

func build(config Config) (immutableSpec, string, error) {
	if !config.Enabled {
		return immutableSpec{}, "", nil
	}
	repository := strings.TrimSpace(config.ArtifactRepository)
	artifactDigest := strings.TrimSpace(config.ArtifactDigest)
	if repository == "" && artifactDigest == "" {
		// Development can use only the embedded enrollment distribution. A
		// production configuration validator requires the complete signed OCI
		// release, so a no-op here is never a production downgrade.
		return immutableSpec{}, "", nil
	}
	values := []string{repository, artifactDigest, config.DistributionDigest,
		config.AgentVersion, config.AgentImage}
	complete := true
	for _, value := range values {
		complete = complete && strings.TrimSpace(value) != ""
	}
	publicKeys := config.PublicKeys
	if len(publicKeys) == 0 && len(strings.TrimSpace(string(config.PublicKey))) != 0 {
		publicKeys = [][]byte{config.PublicKey}
	}
	staticKey := len(publicKeys) != 0
	keyless := strings.TrimSpace(config.CertificateIssuer) != "" || strings.TrimSpace(config.CertificateIdentity) != ""
	if staticKey {
		if keyless {
			return immutableSpec{}, "", errors.New("delivery system release must use either an offline public key or keyless OIDC identity")
		}
		probe := protocol.DeliverySystemVerification{Provider: "cosign", PublicKeys: publicKeys}
		if err := probe.Validate(); err != nil {
			return immutableSpec{}, "", fmt.Errorf("invalid delivery system Cosign public-key set: %w", err)
		}
	} else if strings.TrimSpace(config.CertificateIssuer) == "" || strings.TrimSpace(config.CertificateIdentity) == "" {
		complete = false
	}
	if !complete {
		return immutableSpec{}, "", errors.New("delivery system release configuration is incomplete")
	}
	minimum, maximum := strings.TrimSpace(config.MinimumKubernetes), strings.TrimSpace(config.MaximumKubernetes)
	if !kubernetesMinorPattern.MatchString(minimum) || !kubernetesMinorPattern.MatchString(maximum) {
		return immutableSpec{}, "", errors.New("delivery Kubernetes bounds must be major.minor values")
	}
	artifactURL := repository
	if !strings.HasPrefix(artifactURL, "oci://") {
		artifactURL = "oci://" + artifactURL
	}
	version := strings.TrimSpace(config.Version)
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	agentVersion := strings.TrimSpace(config.AgentVersion)
	if !strings.HasPrefix(agentVersion, "v") {
		agentVersion = "v" + agentVersion
	}
	verification := protocol.DeliverySystemVerification{Provider: "cosign"}
	if staticKey {
		verification.PublicKeys = make([][]byte, len(publicKeys))
		for index := range publicKeys {
			verification.PublicKeys[index] = append([]byte(nil), publicKeys[index]...)
		}
	} else {
		verification.OIDCIdentities = []protocol.DeliveryOIDCIdentity{{
			Issuer: strings.TrimSpace(config.CertificateIssuer), Subject: strings.TrimSpace(config.CertificateIdentity),
		}}
	}
	spec := immutableSpec{
		Version: version, ArtifactURL: artifactURL,
		ArtifactDigest: artifactDigest, DistributionDigest: strings.TrimSpace(config.DistributionDigest),
		AgentVersion: agentVersion, AgentImage: strings.TrimSpace(config.AgentImage),
		MinimumKubernetes: "v" + minimum + ".0", MaximumKubernetes: "v" + maximum + ".999",
		CRDStorageVersion: "v1", Interval: "5m", Timeout: "15m",
		Verification: verification,
	}
	// The app version alone is not a unique delivery-system release identity:
	// installation-specific keyrings and mirror digests can change without a
	// new Astronomer binary. Suffix the immutable spec hash as SemVer build
	// metadata so an operator can stage a new trust configuration as a distinct
	// draft instead of mutating the already-published version in place.
	identityPayload, err := json.Marshal(spec)
	if err != nil {
		return immutableSpec{}, "", fmt.Errorf("encode delivery system release: %w", err)
	}
	identity := sha256.Sum256(identityPayload)
	spec.Version = withSystemReleaseIdentity(version, identity)
	probe := protocol.DeliverySystemReleaseV2{
		Generation: 1, Version: spec.Version, ArtifactURL: spec.ArtifactURL,
		ArtifactDigest: spec.ArtifactDigest, DistributionDigest: spec.DistributionDigest,
		AgentVersion: spec.AgentVersion, AgentImage: spec.AgentImage,
		MinimumKubernetes: spec.MinimumKubernetes, MaximumKubernetes: spec.MaximumKubernetes,
		CRDStorageVersion: spec.CRDStorageVersion, Interval: spec.Interval, Timeout: spec.Timeout,
		Verification: spec.Verification,
	}
	if err := probe.Validate(); err != nil {
		return immutableSpec{}, "", fmt.Errorf("invalid delivery system release: %w", err)
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return immutableSpec{}, "", fmt.Errorf("encode delivery system release: %w", err)
	}
	digest := sha256.Sum256(payload)
	return spec, fmt.Sprintf("sha256:%x", digest), nil
}

func withSystemReleaseIdentity(version string, identity [32]byte) string {
	version = strings.TrimPrefix(version, "v")
	// delivery_system_releases.version is varchar(64). Keep the installation
	// identity bounded while retaining 64 bits of the immutable spec hash; the
	// full spec digest is persisted separately and remains the conflict check.
	metadata := fmt.Sprintf("system.%x", identity[:8])
	if plus := strings.IndexByte(version, '+'); plus >= 0 {
		version = version[:plus] + "+" + metadata + "." + version[plus+1:]
	} else {
		version += "+" + metadata
	}
	return "v" + version
}

// Ensure inserts and promotes exactly one immutable release under a
// transaction-scoped advisory lock. Concurrent server replicas converge on the
// same row on retry after any serializable snapshot conflict; changing a
// published version in place fails closed.
func Ensure(ctx context.Context, pool *pgxpool.Pool, config Config) (bool, error) {
	if pool == nil {
		return false, errors.New("delivery system release database is required")
	}
	spec, specDigest, err := build(config)
	if err != nil || specDigest == "" {
		return false, err
	}
	verification, err := json.Marshal(spec.Verification)
	if err != nil {
		return false, fmt.Errorf("encode verification policy: %w", err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, fmt.Errorf("begin delivery system release transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	created, err := ensureRelease(ctx, sqlc.New(tx), spec, specDigest, verification)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit delivery system release: %w", err)
	}
	return created, nil
}

type bootstrapQueries interface {
	LockDeliverySystemReleaseBootstrap(context.Context) error
	GetDeliverySystemBootstrapReleaseByVersion(context.Context, string) (sqlc.GetDeliverySystemBootstrapReleaseByVersionRow, error)
	CreateDeliverySystemBootstrapRelease(context.Context, sqlc.CreateDeliverySystemBootstrapReleaseParams) (sqlc.CreateDeliverySystemBootstrapReleaseRow, error)
	CountReleasedDeliverySystems(context.Context) (int64, error)
	PromoteDeliverySystemBootstrapRelease(context.Context, string) error
}

func ensureRelease(ctx context.Context, q bootstrapQueries, spec immutableSpec, specDigest string, verification json.RawMessage) (bool, error) {
	if err := q.LockDeliverySystemReleaseBootstrap(ctx); err != nil {
		return false, fmt.Errorf("lock delivery system release: %w", err)
	}
	row, err := q.GetDeliverySystemBootstrapReleaseByVersion(ctx, spec.Version)
	id, currentDigest, state := row.ID, row.SpecDigest, row.State
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		var inserted sqlc.CreateDeliverySystemBootstrapReleaseRow
		inserted, err = q.CreateDeliverySystemBootstrapRelease(ctx, sqlc.CreateDeliverySystemBootstrapReleaseParams{
			Version: spec.Version, ArtifactUrl: spec.ArtifactURL,
			ArtifactDigest: spec.ArtifactDigest, DistributionDigest: spec.DistributionDigest,
			AgentVersion: spec.AgentVersion, AgentImage: spec.AgentImage,
			MinimumKubernetes: spec.MinimumKubernetes, MaximumKubernetes: spec.MaximumKubernetes,
			CrdStorageVersion: spec.CRDStorageVersion, PreviousStorageVersion: spec.PreviousStorageVersion,
			Interval: spec.Interval, Timeout: spec.Timeout,
			VerificationPolicy: verification, SpecDigest: specDigest,
		})
		id, state = inserted.ID, inserted.State
		created = true
	}
	if err != nil {
		return false, fmt.Errorf("load or create delivery system release: %w", err)
	}
	if !created && currentDigest != specDigest {
		return false, fmt.Errorf("delivery system release %s is immutable: configured digest differs", spec.Version)
	}
	if state == "revoked" {
		return false, fmt.Errorf("delivery system release %s is revoked", spec.Version)
	}
	// The very first release is safe to promote because there is no managed cluster set to
	// upgrade. A later server/chart release remains draft until an operator
	// starts the explicit canary/rolling system rollout; startup must never turn
	// a management-plane restart into a simultaneous downstream upgrade.
	releasedCount, err := q.CountReleasedDeliverySystems(ctx)
	if err != nil {
		return false, fmt.Errorf("count released delivery systems: %w", err)
	}
	if state == "draft" && releasedCount == 0 {
		if err := q.PromoteDeliverySystemBootstrapRelease(ctx, id); err != nil {
			return false, fmt.Errorf("promote delivery system release: %w", err)
		}
	}
	return created, nil
}
