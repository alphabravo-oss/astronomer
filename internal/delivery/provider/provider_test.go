package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var (
	testCluster    = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	testDeployment = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	testTarget     = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	testProject    = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	testSource     = uuid.MustParse("55555555-5555-4555-8555-555555555555")
)

type fakeQueries struct {
	rows    []sqlc.ListClusterDeliveryAssignmentsRow
	receipt sqlc.DeliveryAssignmentReceipt
	system  *sqlc.GetClusterDeliverySystemReleaseRow
}

func (f *fakeQueries) ListClusterDeliveryAssignments(context.Context, uuid.UUID) ([]sqlc.ListClusterDeliveryAssignmentsRow, error) {
	return append([]sqlc.ListClusterDeliveryAssignmentsRow(nil), f.rows...), nil
}

func (f *fakeQueries) GetClusterDeliverySystemRelease(context.Context, uuid.UUID) (sqlc.GetClusterDeliverySystemReleaseRow, error) {
	if f.system == nil {
		return sqlc.GetClusterDeliverySystemReleaseRow{}, pgx.ErrNoRows
	}
	return *f.system, nil
}

func (f *fakeQueries) AdvanceDeliveryAssignmentSnapshot(_ context.Context, arg sqlc.AdvanceDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error) {
	if f.receipt.ClusterID == uuid.Nil {
		f.receipt.ClusterID = arg.ClusterID
		f.receipt.DesiredSnapshotGeneration = 1
		f.receipt.CredentialEpoch = 1
	}
	if f.receipt.DesiredContentDigest != "" && (f.receipt.DesiredContentDigest != arg.ContentDigest || f.receipt.CredentialContentDigest != arg.CredentialDigest) {
		f.receipt.DesiredSnapshotGeneration++
		f.receipt.DesiredSnapshotEtag = ""
		if f.receipt.CredentialContentDigest != arg.CredentialDigest {
			f.receipt.CredentialEpoch++
		}
	}
	f.receipt.DesiredContentDigest = arg.ContentDigest
	f.receipt.CredentialContentDigest = arg.CredentialDigest
	return f.receipt, nil
}

func (f *fakeQueries) FinalizeDeliveryAssignmentSnapshot(_ context.Context, arg sqlc.FinalizeDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error) {
	if f.receipt.ClusterID != arg.ClusterID || f.receipt.DesiredSnapshotGeneration != arg.SnapshotGeneration || f.receipt.DesiredContentDigest != arg.ContentDigest {
		return sqlc.DeliveryAssignmentReceipt{}, pgx.ErrNoRows
	}
	f.receipt.DesiredSnapshotEtag = arg.SnapshotEtag
	return f.receipt, nil
}

type fakeDecryptor map[string]string

func (f fakeDecryptor) Decrypt(value string) (string, error) {
	plain, ok := f[value]
	if !ok {
		return "", errors.New("ciphertext rejected")
	}
	return plain, nil
}

type countingDecryptor struct {
	values map[string]string
	calls  int
}

func (d *countingDecryptor) Decrypt(value string) (string, error) {
	d.calls++
	plain, ok := d.values[value]
	if !ok {
		return "", errors.New("ciphertext rejected")
	}
	return plain, nil
}

func TestSnapshotBuildsValidatedAssignmentAndNotModified(t *testing.T) {
	queries := &fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{gitRow(t)}}
	decryptor := &countingDecryptor{values: map[string]string{"sealed": `{"username":"robot","password":"private-key"}`, "sealed-ca": "test-ca"}}
	p := New(queries, decryptor)
	request := validRequest()

	first, err := p.Snapshot(context.Background(), testCluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("provider returned invalid snapshot: %v", err)
	}
	capabilities := agentdelivery.Capabilities{
		SourceKinds: []protocol.DeliverySourceKind{protocol.DeliverySourceGit}, RendererKinds: []protocol.DeliveryRendererKind{protocol.DeliveryRendererKustomize},
		FluxAPIVersions: []string{"source.toolkit.fluxcd.io/v1", "kustomize.toolkit.fluxcd.io/v1"},
		NamespaceScope:  true, NoCrossNamespaceRefs: true, NoRemoteKustomizeBases: true,
	}
	if err := agentdelivery.ValidateAssignment(first.Assignments[0], capabilities, agentdelivery.ValidationPolicy{}); err != nil {
		t.Fatalf("server assignment was rejected by agent: %v", err)
	}
	if len(first.Assignments) != 1 || first.Assignments[0].Source.Revision != strings.Repeat("b", 40) {
		t.Fatalf("unexpected assignments: %#v", first.Assignments)
	}
	credential := first.Assignments[0].Credential
	if credential == nil || string(credential.Data["password"]) != "private-key" || string(credential.Data["ca.crt"]) != "test-ca" {
		t.Fatalf("credential was not materialized: %#v", credential)
	}

	request.AckedSnapshotGeneration = first.SnapshotGeneration
	request.AckedETag = first.ETag
	second, err := p.Snapshot(context.Background(), testCluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || second.FullSnapshot || len(second.Assignments) != 0 || second.ETag != first.ETag {
		t.Fatalf("expected metadata-only not-modified response: %#v", second)
	}
	if err := second.Validate(); err != nil {
		t.Fatalf("not-modified response is invalid: %v", err)
	}
	if decryptor.calls != 2 {
		t.Fatalf("not-modified response decrypted credential material; calls=%d, want 2 from first snapshot only", decryptor.calls)
	}
}

func TestSnapshotRejectsPayloadClusterIdentityMismatch(t *testing.T) {
	p := New(&fakeQueries{}, nil)
	request := validRequest()
	request.ClusterID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	_, err := p.Snapshot(context.Background(), testCluster, request)
	if !errors.Is(err, ErrClusterIdentityMismatch) {
		t.Fatalf("got %v, want identity mismatch", err)
	}
}

func TestSnapshotCredentialRotationUsesEpochWithoutHashingSecret(t *testing.T) {
	row := gitRow(t)
	queries := &fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}
	decryptor := fakeDecryptor{"sealed": `{"token":"first"}`, "sealed-ca": "ca"}
	p := New(queries, decryptor)
	first, err := p.Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}

	// Ciphertext changes without the mandatory epoch bump: no digest changes,
	// proving neither plaintext nor ciphertext contributes to the ETag.
	queries.rows[0].CredentialEncrypted = "rotated"
	decryptor["rotated"] = `{"token":"second"}`
	unchanged, err := p.Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.ETag != first.ETag {
		t.Fatal("secret bytes affected the assignment ETag")
	}

	queries.rows[0].CredentialEpoch++
	rotated, err := p.Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ETag == first.ETag || rotated.CredentialEpoch <= first.CredentialEpoch {
		t.Fatal("credential epoch bump did not advance the snapshot")
	}
}

func TestSnapshotRejectsCrossClusterRowAndDecryptFailure(t *testing.T) {
	row := gitRow(t)
	row.ClusterID = uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	p := New(&fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}, fakeDecryptor{})
	if _, err := p.Snapshot(context.Background(), testCluster, validRequest()); err == nil || !strings.Contains(err.Error(), "crossed") {
		t.Fatalf("expected cross-cluster rejection, got %v", err)
	}

	row.ClusterID = testCluster
	p = New(&fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}, fakeDecryptor{})
	if _, err := p.Snapshot(context.Background(), testCluster, validRequest()); err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("expected fail-closed decrypt rejection, got %v", err)
	}
}

func TestSnapshotBuildsDeletionTombstone(t *testing.T) {
	row := gitRow(t)
	row.Action = "delete"
	queries := &fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}
	response, err := New(queries, nil).Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Assignments) != 0 || len(response.Deletions) != 1 || response.Deletions[0].DeploymentID != testDeployment.String() {
		t.Fatalf("unexpected tombstone response: %#v", response)
	}
}

func TestSnapshotIncludesSignedSystemReleaseAndWriteOnlyRegistryCredential(t *testing.T) {
	verification, _ := json.Marshal(protocol.DeliverySystemVerification{Provider: "cosign", OIDCIdentities: []protocol.DeliveryOIDCIdentity{{
		Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/example/release/.github/workflows/release.yaml@refs/tags/v1.0.0",
	}}})
	queries := &fakeQueries{system: &sqlc.GetClusterDeliverySystemReleaseRow{
		Version: "v1.0.0", ArtifactUrl: "oci://registry.example.test/astronomer/system",
		ArtifactDigest: "sha256:" + strings.Repeat("a", 64), DistributionDigest: "sha256:" + strings.Repeat("b", 64),
		AgentVersion: "v1.0.0", AgentImage: "registry.example.test/astronomer/agent@sha256:" + strings.Repeat("c", 64),
		MinimumKubernetes: "v1.33.0", MaximumKubernetes: "v1.35.99", CrdStorageVersion: "v1",
		Interval: "5m", Timeout: "15m", VerificationPolicy: verification,
		RegistryCredentialEncrypted: "sealed-system", CredentialEpoch: 7, AssignmentGeneration: 4,
	}}
	provider := New(queries, fakeDecryptor{"sealed-system": `{"auths":{"registry.example.test":{"auth":"opaque"}}}`})
	response, err := provider.Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.System == nil || response.System.Generation != 4 || response.System.Credential == nil || response.System.Credential.Version != 7 {
		t.Fatalf("system release was not hydrated: %#v", response.System)
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	firstETag := response.ETag
	queries.system.RegistryCredentialEncrypted = "rotated-system"
	provider.decryptor = fakeDecryptor{"rotated-system": `{"auths":{"registry.example.test":{"auth":"different"}}}`}
	unchanged, err := provider.Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.ETag != firstETag {
		t.Fatal("system registry credential bytes affected the snapshot ETag")
	}
	queries.system.CredentialEpoch++
	rotated, err := provider.Snapshot(context.Background(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ETag == firstETag {
		t.Fatal("system registry credential epoch did not invalidate the snapshot")
	}
}

func validRequest() protocol.DeliveryStateRequestV2 {
	return protocol.DeliveryStateRequestV2{
		ClusterID: testCluster.String(), ProtocolVersion: protocol.DeliveryProtocolVersion,
	}
}

func gitRow(t *testing.T) sqlc.ListClusterDeliveryAssignmentsRow {
	t.Helper()
	renderer := model.RendererSpec{Kind: model.RendererKustomize, Kustomize: &model.KustomizeSpec{
		Path: "./clusters/base", TargetNamespace: "apps",
	}}
	policy := model.ReconciliationPolicy{
		Interval: model.Duration(10 * time.Minute), RetryInterval: model.Duration(time.Minute),
		Timeout: model.Duration(10 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
	}
	trust := model.TrustPolicy{AllowUnsigned: true}
	return sqlc.ListClusterDeliveryAssignmentsRow{
		ID: testDeployment, TargetID: testTarget, ClusterID: testCluster, ProjectID: testProject,
		SourceID: testSource, DesiredGeneration: 1, DesiredSpecDigest: "sha256:" + strings.Repeat("a", 64),
		DesiredRevision: strings.Repeat("b", 40), ResolvedRevision: strings.Repeat("b", 40),
		ArtifactDigest: "sha256:" + strings.Repeat("c", 64), Action: "apply", Phase: "applying",
		Renderer: string(model.RendererKustomize), Scope: string(model.ScopeNamespace),
		RendererSpec: mustJSON(t, renderer), ReconciliationPolicy: mustJSON(t, policy),
		SourceType: string(model.SourceGit), Url: "https://git.example.test/platform/apps.git",
		AuthMode: string(model.AuthBasic), CredentialEpoch: 1, CredentialEncrypted: "sealed",
		CaBundleEncrypted: "sealed-ca", TrustPolicy: mustJSON(t, trust),
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
