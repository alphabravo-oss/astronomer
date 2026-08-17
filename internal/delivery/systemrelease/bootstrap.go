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
	values := []string{config.ArtifactRepository, config.ArtifactDigest, config.DistributionDigest,
		config.AgentVersion, config.AgentImage, config.CertificateIssuer, config.CertificateIdentity}
	complete := true
	any := false
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		complete = complete && trimmed != ""
		any = any || trimmed != ""
	}
	if !any {
		// Development can use only the embedded enrollment distribution. A
		// production configuration validator requires the complete signed OCI
		// release, so a no-op here is never a production downgrade.
		return immutableSpec{}, "", nil
	}
	if !complete {
		return immutableSpec{}, "", errors.New("delivery system release configuration is incomplete")
	}
	minimum, maximum := strings.TrimSpace(config.MinimumKubernetes), strings.TrimSpace(config.MaximumKubernetes)
	if !kubernetesMinorPattern.MatchString(minimum) || !kubernetesMinorPattern.MatchString(maximum) {
		return immutableSpec{}, "", errors.New("delivery Kubernetes bounds must be major.minor values")
	}
	artifactURL := strings.TrimSpace(config.ArtifactRepository)
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
	spec := immutableSpec{
		Version: version, ArtifactURL: artifactURL,
		ArtifactDigest: strings.TrimSpace(config.ArtifactDigest), DistributionDigest: strings.TrimSpace(config.DistributionDigest),
		AgentVersion: agentVersion, AgentImage: strings.TrimSpace(config.AgentImage),
		MinimumKubernetes: "v" + minimum + ".0", MaximumKubernetes: "v" + maximum + ".999",
		CRDStorageVersion: "v1", Interval: "5m", Timeout: "15m",
		Verification: protocol.DeliverySystemVerification{Provider: "cosign", OIDCIdentities: []protocol.DeliveryOIDCIdentity{{
			Issuer: strings.TrimSpace(config.CertificateIssuer), Subject: strings.TrimSpace(config.CertificateIdentity),
		}}},
	}
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

// Ensure inserts and promotes exactly one immutable release under a
// transaction-scoped advisory lock. Concurrent server replicas converge on the
// same row; changing a published version in place fails closed.
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('astronomer-delivery-system-release', 0))`); err != nil {
		return false, fmt.Errorf("lock delivery system release: %w", err)
	}
	var id, currentDigest, state string
	err = tx.QueryRow(ctx, `SELECT id::text, spec_digest, state FROM delivery_system_releases WHERE version = $1`, spec.Version).
		Scan(&id, &currentDigest, &state)
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO delivery_system_releases (
				version, artifact_url, artifact_digest, distribution_digest,
				agent_version, agent_image, minimum_kubernetes, maximum_kubernetes,
				crd_storage_version, previous_storage_version, interval, timeout,
				verification_policy, spec_digest
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id::text, state`,
			spec.Version, spec.ArtifactURL, spec.ArtifactDigest, spec.DistributionDigest,
			spec.AgentVersion, spec.AgentImage, spec.MinimumKubernetes, spec.MaximumKubernetes,
			spec.CRDStorageVersion, spec.PreviousStorageVersion, spec.Interval, spec.Timeout,
			verification, specDigest).Scan(&id, &state)
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
	var releasedCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM delivery_system_releases WHERE state='released'`).Scan(&releasedCount); err != nil {
		return false, fmt.Errorf("count released delivery systems: %w", err)
	}
	if state == "draft" && releasedCount == 0 {
		if _, err := tx.Exec(ctx, `UPDATE delivery_system_releases SET state='released', released_at=COALESCE(released_at,now()), retired_at=NULL WHERE id::text=$1`, id); err != nil {
			return false, fmt.Errorf("promote delivery system release: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit delivery system release: %w", err)
	}
	return created, nil
}
