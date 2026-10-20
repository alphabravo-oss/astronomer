package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func signingSnapshot(t *testing.T) *resolver.PublicKeySnapshot {
	t.Helper()
	_, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := resolver.CaptureMountedPublicKey(source, "qa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(snapshot.Close)
	return snapshot
}
func TestBuiltinPublicKeyRotationUsesExistingReceipt(t *testing.T) {
	row := builtinOCIRow(t)
	row.TrustPolicy = mustJSON(t, model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"})
	queries := &fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}
	authorize := func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, protocol.DeliveryAssignmentV2) (bool, error) {
		return true, nil
	}
	snapshot := signingSnapshot(t)
	ca := fixtureCA(t)
	state, err := newWithBuiltinPublicKey(queries, nil, snapshot, ca, authorize)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.Snapshot(t.Context(), testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Assignments[0].Credential.Data["caFile"]) != string(ca) || first.Assignments[0].Source.CredentialSecret == "" || first.Assignments[0].Source.TrustSecret == "" {
		t.Fatal("combined CA and public signer bindings were not hydrated")
	}
	request := validRequest()
	request.AckedSnapshotGeneration = first.SnapshotGeneration
	request.AckedETag = first.ETag
	secondState, err := newWithBuiltinPublicKey(queries, nil, signingSnapshot(t), ca, authorize)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondState.Snapshot(t.Context(), testCluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.NotModified || second.CredentialEpoch <= first.CredentialEpoch || second.Assignments[0].Credential.Version != second.CredentialEpoch {
		t.Fatal("signing key rotation failed to advance receipt")
	}
	clear(second.Assignments[0].Credential.Data["trust.cosign.pub"])
	again, err := secondState.Snapshot(t.Context(), testCluster, validRequest())
	if err != nil || len(again.Assignments[0].Credential.Data["trust.cosign.pub"]) == 0 {
		t.Fatal("response mutation changed captured signing key")
	}
	for _, options := range []distribution.Options{{Development: true}, {Profile: "builtins", Policy: model.TrustPolicy{AllowUnsigned: true}}, {Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "wrong"}}, {Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "vendor", Issuer: "issuer"}}, {Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, TrustDirectory: t.TempDir()}} {
		if _, _, err := NewBuiltinQAConsumer(queries, nil, snapshot, nil, options); err == nil {
			t.Fatal("QA factory accepted unbound/unsigned policy")
		}
	}
	snapshot.Close()
	if _, err := newWithBuiltinPublicKey(queries, nil, snapshot, nil, authorize); err == nil {
		t.Fatal("closed public snapshot accepted")
	}
}
