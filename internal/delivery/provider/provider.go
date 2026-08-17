// Package provider builds authenticated, immutable delivery snapshots for
// downstream agents. It is deliberately independent of the tunnel transport:
// the tunnel binds a request to a cluster identity and calls Snapshot.
package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const maxSnapshotFinalizeAttempts = 3

var (
	ErrClusterIdentityMismatch = errors.New("delivery request cluster does not match authenticated session")
	ErrSnapshotChanged         = errors.New("delivery snapshot changed while it was being finalized")
)

type Querier interface {
	ListClusterDeliveryAssignments(context.Context, uuid.UUID) ([]sqlc.ListClusterDeliveryAssignmentsRow, error)
	GetClusterDeliverySystemRelease(context.Context, uuid.UUID) (sqlc.GetClusterDeliverySystemReleaseRow, error)
	AdvanceDeliveryAssignmentSnapshot(context.Context, sqlc.AdvanceDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error)
	FinalizeDeliveryAssignmentSnapshot(context.Context, sqlc.FinalizeDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error)
}

type Decryptor interface {
	Decrypt(string) (string, error)
}

type Provider struct {
	queries   Querier
	decryptor Decryptor
}

func New(queries Querier, decryptor Decryptor) *Provider {
	return &Provider{queries: queries, decryptor: decryptor}
}

// Snapshot returns only assignments released for authenticatedCluster. A
// caller must pass the identity bound to the authenticated tunnel session, not
// one derived from request payload bytes.
func (p *Provider) Snapshot(ctx context.Context, authenticatedCluster uuid.UUID, request protocol.DeliveryStateRequestV2) (final protocol.DeliveryStateResponseV2, finalErr error) {
	defer func() {
		result := "success"
		switch {
		case finalErr == nil && final.NotModified:
			result = "not_modified"
		case errors.Is(finalErr, ErrClusterIdentityMismatch):
			result = "identity_mismatch"
		case errors.Is(finalErr, ErrSnapshotChanged):
			result = "changed"
		case finalErr != nil:
			result = "failure"
		}
		bytesCount := -1
		objects := -1
		if finalErr == nil {
			objects = len(final.Assignments) + len(final.Deletions)
			if encoded, err := json.Marshal(final); err == nil {
				bytesCount = len(encoded)
				clear(encoded)
			}
		}
		deliverymetrics.ObserveSnapshot(result, bytesCount, objects)
	}()
	if p == nil || p.queries == nil {
		return protocol.DeliveryStateResponseV2{}, errors.New("delivery provider is not configured")
	}
	if authenticatedCluster == uuid.Nil {
		return protocol.DeliveryStateResponseV2{}, errors.New("authenticated cluster is required")
	}
	if err := request.Validate(); err != nil {
		return protocol.DeliveryStateResponseV2{}, fmt.Errorf("validate delivery request: %w", err)
	}
	if request.ClusterID != authenticatedCluster.String() {
		return protocol.DeliveryStateResponseV2{}, ErrClusterIdentityMismatch
	}

	for attempt := 0; attempt < maxSnapshotFinalizeAttempts; attempt++ {
		response, rows, systemRelease, contentDigest, credentialDigest, err := p.build(ctx, authenticatedCluster)
		if err != nil {
			return protocol.DeliveryStateResponseV2{}, err
		}
		receipt, err := p.queries.AdvanceDeliveryAssignmentSnapshot(ctx, sqlc.AdvanceDeliveryAssignmentSnapshotParams{
			ClusterID:        authenticatedCluster,
			ContentDigest:    contentDigest,
			CredentialDigest: credentialDigest,
		})
		if err != nil {
			return protocol.DeliveryStateResponseV2{}, fmt.Errorf("advance delivery snapshot: %w", err)
		}
		response.SnapshotGeneration = receipt.DesiredSnapshotGeneration
		response.CredentialEpoch = receipt.CredentialEpoch
		response.ETag, err = response.CanonicalETag()
		if err != nil {
			return protocol.DeliveryStateResponseV2{}, err
		}
		_, err = p.queries.FinalizeDeliveryAssignmentSnapshot(ctx, sqlc.FinalizeDeliveryAssignmentSnapshotParams{
			SnapshotEtag:       response.ETag,
			ClusterID:          authenticatedCluster,
			SnapshotGeneration: response.SnapshotGeneration,
			ContentDigest:      contentDigest,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return protocol.DeliveryStateResponseV2{}, fmt.Errorf("finalize delivery snapshot: %w", err)
		}
		if request.AckedSnapshotGeneration == response.SnapshotGeneration && request.AckedETag == response.ETag {
			return protocol.DeliveryStateResponseV2{
				ProtocolVersion:    protocol.DeliveryProtocolVersion,
				SnapshotGeneration: response.SnapshotGeneration,
				ETag:               response.ETag,
				NotModified:        true,
				CredentialEpoch:    response.CredentialEpoch,
			}, nil
		}
		if err := p.hydrateCredentials(&response, rows, systemRelease); err != nil {
			return protocol.DeliveryStateResponseV2{}, err
		}
		if err := response.Validate(); err != nil {
			return protocol.DeliveryStateResponseV2{}, fmt.Errorf("built invalid delivery snapshot: %w", err)
		}
		return response, nil
	}
	return protocol.DeliveryStateResponseV2{}, ErrSnapshotChanged
}

func (p *Provider) build(ctx context.Context, clusterID uuid.UUID) (protocol.DeliveryStateResponseV2, []sqlc.ListClusterDeliveryAssignmentsRow, *sqlc.GetClusterDeliverySystemReleaseRow, string, string, error) {
	rows, err := p.queries.ListClusterDeliveryAssignments(ctx, clusterID)
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", fmt.Errorf("list cluster delivery assignments: %w", err)
	}
	if len(rows) > protocol.MaxDeliveryAssignments+protocol.MaxDeliveryDeletions {
		return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", errors.New("delivery snapshot exceeds object limit")
	}
	response := protocol.DeliveryStateResponseV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion,
		FullSnapshot:    true,
		Assignments:     make([]protocol.DeliveryAssignmentV2, 0, len(rows)),
		Deletions:       make([]protocol.DeliveryDeletionV2, 0),
	}
	type credentialGeneration struct {
		DeploymentID string `json:"deployment_id"`
		Epoch        int64  `json:"epoch"`
	}
	credentialGenerations := make([]credentialGeneration, 0, len(rows))
	for index := range rows {
		row := rows[index]
		if row.ClusterID != clusterID {
			return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", fmt.Errorf("assignment %s crossed the authenticated cluster boundary", row.ID)
		}
		if row.Action == "delete" {
			response.Deletions = append(response.Deletions, protocol.DeliveryDeletionV2{
				DeploymentID: row.ID.String(), Generation: row.DesiredGeneration,
				SpecDigest: row.DesiredSpecDigest,
			})
			continue
		}
		assignment, err := p.assignmentMetadata(row)
		if err != nil {
			return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", fmt.Errorf("assignment %s: %w", row.ID, err)
		}
		response.Assignments = append(response.Assignments, assignment)
		credentialGenerations = append(credentialGenerations, credentialGeneration{DeploymentID: row.ID.String(), Epoch: row.CredentialEpoch})
	}
	sort.Slice(response.Assignments, func(i, j int) bool {
		return response.Assignments[i].DeploymentID < response.Assignments[j].DeploymentID
	})
	sort.Slice(response.Deletions, func(i, j int) bool { return response.Deletions[i].DeploymentID < response.Deletions[j].DeploymentID })
	sort.Slice(credentialGenerations, func(i, j int) bool {
		return credentialGenerations[i].DeploymentID < credentialGenerations[j].DeploymentID
	})

	var systemRelease *sqlc.GetClusterDeliverySystemReleaseRow
	row, err := p.queries.GetClusterDeliverySystemRelease(ctx, clusterID)
	if err == nil {
		system, buildErr := systemReleaseMetadata(row)
		if buildErr != nil {
			return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", buildErr
		}
		response.System = &system
		systemRelease = &row
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", fmt.Errorf("get cluster delivery system release: %w", err)
	}

	// Compute intent and credential-generation identities separately. The
	// intent digest excludes credential bytes. The credential digest contains
	// only monotonically increasing epochs, never an offline-verifiable secret.
	digestProjection := response
	digestProjection.SnapshotGeneration = 1
	digestProjection.CredentialEpoch = 0
	contentDigest, err := digestProjection.CanonicalETag()
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", err
	}
	credentialProjection := struct {
		Assignments []credentialGeneration `json:"assignments"`
		SystemEpoch int64                  `json:"system_epoch"`
	}{Assignments: credentialGenerations}
	if systemRelease != nil {
		credentialProjection.SystemEpoch = systemRelease.CredentialEpoch
	}
	credentialDigest, err := canonicalDigest(credentialProjection)
	if err != nil {
		return protocol.DeliveryStateResponseV2{}, nil, nil, "", "", err
	}
	return response, rows, systemRelease, contentDigest, credentialDigest, nil
}

func systemReleaseMetadata(row sqlc.GetClusterDeliverySystemReleaseRow) (protocol.DeliverySystemReleaseV2, error) {
	var verification protocol.DeliverySystemVerification
	if err := decodeStrict(row.VerificationPolicy, &verification); err != nil {
		return protocol.DeliverySystemReleaseV2{}, fmt.Errorf("decode system verification policy: %w", err)
	}
	release := protocol.DeliverySystemReleaseV2{
		Generation: row.AssignmentGeneration, Version: row.Version,
		ArtifactURL: row.ArtifactUrl, ArtifactDigest: row.ArtifactDigest,
		DistributionDigest: row.DistributionDigest, AgentVersion: row.AgentVersion, AgentImage: row.AgentImage,
		MinimumKubernetes: row.MinimumKubernetes, MaximumKubernetes: row.MaximumKubernetes,
		CRDStorageVersion: row.CrdStorageVersion, PreviousStorageVersion: row.PreviousStorageVersion,
		Interval: row.Interval, Timeout: row.Timeout, Verification: verification,
	}
	if err := release.Validate(); err != nil {
		return protocol.DeliverySystemReleaseV2{}, fmt.Errorf("validate system release: %w", err)
	}
	return release, nil
}

func (p *Provider) assignmentMetadata(row sqlc.ListClusterDeliveryAssignmentsRow) (protocol.DeliveryAssignmentV2, error) {
	var renderer model.RendererSpec
	if err := decodeStrict(row.RendererSpec, &renderer); err != nil {
		return protocol.DeliveryAssignmentV2{}, fmt.Errorf("decode renderer: %w", err)
	}
	if err := renderer.Validate(); err != nil {
		return protocol.DeliveryAssignmentV2{}, fmt.Errorf("validate renderer: %w", err)
	}
	if string(renderer.Kind) != row.Renderer {
		return protocol.DeliveryAssignmentV2{}, errors.New("renderer column and immutable renderer spec disagree")
	}
	var policy model.ReconciliationPolicy
	if err := decodeStrict(row.ReconciliationPolicy, &policy); err != nil {
		return protocol.DeliveryAssignmentV2{}, fmt.Errorf("decode reconciliation policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return protocol.DeliveryAssignmentV2{}, fmt.Errorf("validate reconciliation policy: %w", err)
	}
	var trust model.TrustPolicy
	if err := decodeStrict(row.TrustPolicy, &trust); err != nil {
		return protocol.DeliveryAssignmentV2{}, fmt.Errorf("decode trust policy: %w", err)
	}
	if err := trust.Validate(); err != nil {
		return protocol.DeliveryAssignmentV2{}, fmt.Errorf("validate trust policy: %w", err)
	}

	names := protocol.ObjectNames(row.ProjectID.String(), row.ID.String())
	source := protocol.DeliverySourceV2{
		Kind:     protocol.DeliverySourceKind(row.SourceType),
		URL:      row.Url,
		Revision: row.ResolvedRevision,
		Digest:   row.ArtifactDigest,
	}
	if !trust.AllowUnsigned {
		source.Verify = verification(trust)
	}
	if row.AuthMode != string(model.AuthNone) || row.CaBundleEncrypted != "" {
		source.CredentialSecret = names.AuthSecret
	}
	if !trust.AllowUnsigned && trust.KeyRef != "" {
		source.TrustSecret = names.TrustSecret
	}
	assignment := protocol.DeliveryAssignmentV2{
		DeploymentID: row.ID.String(), TargetID: row.TargetID.String(), ProjectID: row.ProjectID.String(),
		Generation: row.DesiredGeneration, SpecDigest: row.DesiredSpecDigest,
		Action: protocol.DeliveryAction(row.Action), Scope: protocol.DeliveryScope(row.Scope),
		Source: source,
		Policy: protocol.DeliveryReconciliationPolicy{
			Interval: time.Duration(policy.Interval).String(), RetryInterval: time.Duration(policy.RetryInterval).String(),
			Timeout: time.Duration(policy.Timeout).String(), Drift: string(policy.Drift), Prune: policy.Prune,
		},
	}
	switch renderer.Kind {
	case model.RendererKustomize:
		assignment.Source.Path = strings.TrimPrefix(renderer.Kustomize.Path, "./")
		assignment.Renderer = protocol.DeliveryRendererV2{Kind: protocol.DeliveryRendererKustomize, Kustomize: &protocol.DeliveryKustomizeRenderer{
			TargetNamespace: renderer.Kustomize.TargetNamespace, ServiceAccount: names.Applier,
			Prune: policy.Prune, Wait: policy.Wait, Patches: append([]string(nil), renderer.Kustomize.Patches...),
		}}
	case model.RendererHelm:
		assignment.Source.Chart = renderer.Helm.Chart
		assignment.Renderer = protocol.DeliveryRendererV2{Kind: protocol.DeliveryRendererHelm, Helm: &protocol.DeliveryHelmRenderer{
			Chart: renderer.Helm.Chart, Version: renderer.Helm.ChartVersion, ReleaseName: renderer.Helm.ReleaseName,
			TargetNamespace: renderer.Helm.TargetNamespace, ServiceAccount: names.Applier,
			Values: append(json.RawMessage(nil), renderer.Helm.Values...), InstallRetries: int(renderer.Helm.InstallRetries),
			UpgradeRetries: int(renderer.Helm.UpgradeRetries), UpgradeRemediation: "rollback", EnableTests: renderer.Helm.Test,
			DriftMode: driftMode(policy.Drift),
		}}
	}
	if row.SourceType == string(model.SourceOCIArtifact) {
		assignment.Source.Revision = row.ArtifactDigest
	}
	if err := assignment.Validate(); err != nil {
		zeroCredential(assignment.Credential)
		return protocol.DeliveryAssignmentV2{}, err
	}
	return assignment, nil
}

func (p *Provider) hydrateCredentials(response *protocol.DeliveryStateResponseV2, rows []sqlc.ListClusterDeliveryAssignmentsRow, systemRelease *sqlc.GetClusterDeliverySystemReleaseRow) error {
	if response == nil {
		return errors.New("delivery response is required")
	}
	if systemRelease != nil && systemRelease.RegistryCredentialEncrypted != "" {
		if response.System == nil || p.decryptor == nil {
			return errors.New("system registry credential decryptor is unavailable")
		}
		plain, err := p.decryptor.Decrypt(systemRelease.RegistryCredentialEncrypted)
		if err != nil {
			return fmt.Errorf("decrypt system registry credential: %w", err)
		}
		material := []byte(plain)
		if !json.Valid(material) {
			clear(material)
			return errors.New("system registry credential is not valid docker configuration JSON")
		}
		response.System.Credential = &protocol.DeliveryCredentialMaterial{
			Version: systemRelease.CredentialEpoch,
			Data:    map[string][]byte{".dockerconfigjson": material},
		}
	}
	assignments := make(map[string]*protocol.DeliveryAssignmentV2, len(response.Assignments))
	for index := range response.Assignments {
		assignments[response.Assignments[index].DeploymentID] = &response.Assignments[index]
	}
	for index := range rows {
		row := rows[index]
		assignment := assignments[row.ID.String()]
		if assignment == nil {
			continue
		}
		needsAuth := assignment.Source.CredentialSecret != ""
		needsTrust := assignment.Source.TrustSecret != ""
		if !needsAuth && !needsTrust {
			continue
		}
		if p.decryptor == nil {
			return errors.New("source credential decryptor is unavailable")
		}
		data, err := p.decryptCredential(row.SourceType, row.CredentialEncrypted, row.CaBundleEncrypted)
		if err != nil {
			return err
		}
		hasAuth, hasTrust := false, false
		for key := range data {
			if strings.HasPrefix(key, "trust.") {
				hasTrust = true
			} else {
				hasAuth = true
			}
		}
		if len(data) == 0 || hasAuth != needsAuth || hasTrust != needsTrust {
			zeroMap(data)
			return fmt.Errorf("source %s credential shape does not match its non-secret metadata", row.SourceID)
		}
		assignment.Credential = &protocol.DeliveryCredentialMaterial{Version: row.CredentialEpoch, Data: data}
	}
	return nil
}

func (p *Provider) decryptCredential(sourceType, encrypted, encryptedCA string) (map[string][]byte, error) {
	data := make(map[string][]byte)
	if encrypted != "" {
		plain, err := p.decryptor.Decrypt(encrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt source credential: %w", err)
		}
		buffer := []byte(plain)
		var values map[string]string
		err = decodeStrict(buffer, &values)
		clear(buffer)
		if err != nil {
			return nil, fmt.Errorf("decode source credential: %w", err)
		}
		for key, value := range values {
			data[key] = []byte(value)
		}
	}
	if encryptedCA != "" {
		plain, err := p.decryptor.Decrypt(encryptedCA)
		if err != nil {
			zeroMap(data)
			return nil, fmt.Errorf("decrypt source CA: %w", err)
		}
		caKey := "ca.crt"
		if sourceType == string(model.SourceHelmHTTP) || sourceType == string(model.SourceHelmOCI) || sourceType == string(model.SourceOCIArtifact) {
			caKey = "caFile"
		}
		data[caKey] = []byte(plain)
	}
	return data, nil
}

func verification(trust model.TrustPolicy) *protocol.DeliveryVerifyV2 {
	provider := "cosign"
	if trust.Provider == model.SignatureGit {
		provider = "pgp"
	}
	result := &protocol.DeliveryVerifyV2{Provider: provider}
	if trust.Provider == model.SignatureGit {
		result.Mode = "HEAD"
	}
	if trust.Provider == model.SignatureCosignKeyless {
		result.OIDCIdentities = []protocol.DeliveryOIDCIdentity{{Issuer: trust.Issuer, Subject: trust.Identity}}
	}
	return result
}

func driftMode(policy model.DriftPolicy) string {
	switch policy {
	case model.DriftRepair:
		return "enabled"
	case model.DriftDetect:
		return "warn"
	default:
		return "disabled"
	}
}

func canonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}

func zeroCredential(credential *protocol.DeliveryCredentialMaterial) {
	if credential != nil {
		zeroMap(credential.Data)
	}
}

func zeroMap(values map[string][]byte) {
	for key := range values {
		clear(values[key])
		delete(values, key)
	}
}
