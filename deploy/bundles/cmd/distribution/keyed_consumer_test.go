package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/provider"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type keyedConsumerQueries struct {
	rows    []sqlc.ListClusterDeliveryAssignmentsRow
	receipt sqlc.DeliveryAssignmentReceipt
}

func (q *keyedConsumerQueries) ListClusterDeliveryAssignments(context.Context, sqlc.ListClusterDeliveryAssignmentsParams) ([]sqlc.ListClusterDeliveryAssignmentsRow, error) {
	return append([]sqlc.ListClusterDeliveryAssignmentsRow(nil), q.rows...), nil
}
func (q *keyedConsumerQueries) GetClusterDeliverySystemRelease(context.Context, uuid.UUID) (sqlc.GetClusterDeliverySystemReleaseRow, error) {
	return sqlc.GetClusterDeliverySystemReleaseRow{}, pgx.ErrNoRows
}
func (q *keyedConsumerQueries) AdvanceDeliveryAssignmentSnapshot(_ context.Context, args sqlc.AdvanceDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error) {
	if q.receipt.ClusterID == uuid.Nil {
		q.receipt.ClusterID = args.ClusterID
		q.receipt.DesiredSnapshotGeneration = 1
		q.receipt.CredentialEpoch = 1
	}
	if q.receipt.DesiredContentDigest != "" && (q.receipt.DesiredContentDigest != args.ContentDigest || q.receipt.CredentialContentDigest != args.CredentialDigest) {
		q.receipt.DesiredSnapshotGeneration++
		if q.receipt.CredentialContentDigest != args.CredentialDigest {
			q.receipt.CredentialEpoch++
		}
	}
	q.receipt.DesiredContentDigest = args.ContentDigest
	q.receipt.CredentialContentDigest = args.CredentialDigest
	return q.receipt, nil
}
func (q *keyedConsumerQueries) FinalizeDeliveryAssignmentSnapshot(_ context.Context, args sqlc.FinalizeDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error) {
	if args.ClusterID != q.receipt.ClusterID || args.SnapshotGeneration != q.receipt.DesiredSnapshotGeneration || args.ContentDigest != q.receipt.DesiredContentDigest {
		return sqlc.DeliveryAssignmentReceipt{}, pgx.ErrNoRows
	}
	q.receipt.DesiredSnapshotEtag = args.SnapshotEtag
	return q.receipt, nil
}

// This extends real selected-Cosign publication into the constructor-only
// keyed consumer. It does not exercise production server startup or vendor OIDC.
func verifyActualKeyedConsumer(t *testing.T, index subjectIndex, client *http.Client, trustDirectory string) {
	t.Helper()
	verifyActualKeyedConsumerOptions(t, index, client, trustDirectory, nil, httpclient.DestinationPolicy{})
}
func verifyActualKeyedConsumerOptions(t *testing.T, index subjectIndex, client *http.Client, trustDirectory string, mirror *distribution.MirrorPlan, network httpclient.DestinationPolicy) {
	t.Helper()
	snapshot, err := resolver.CaptureMountedPublicKey(trustDirectory, "qa")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	queries := &keyedConsumerQueries{}
	state, loader, err := provider.NewBuiltinQAConsumer(queries, nil, snapshot, nil, distribution.Options{Repository: index.Repository, Digest: index.Digest, CatalogDigest: index.CatalogDigest, Release: index.Release, Profile: "builtins", Policy: model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}, Client: client, MirrorPlan: mirror, Network: network})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	namespace := uuid.MustParse("2b079af7-4a47-4dbe-984b-a67d78ab6909")
	cluster := uuid.New()
	project := uuid.NewSHA1(namespace, []byte("project\x00"+cluster.String()))
	encode := func(value any) json.RawMessage {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	for _, component := range catalog.Components {
		if mirror != nil && (!strings.HasPrefix(component.Source.URL, "oci://1.1.1.1/mirror/") || strings.Contains(string(encode(component.Values)), "1.1.1.1/fixture@") || strings.Contains(string(encode(component.Values)), "1.1.1.1/distribution/assets/")) {
			t.Fatal("public chart/image location survived qualified mirror projection")
		}
		source := uuid.NewSHA1(namespace, []byte("source\x00"+project.String()+"\x00"+component.Source.URL+"\x00"+catalog.DistributionIdentity))
		row := sqlc.ListClusterDeliveryAssignmentsRow{ID: uuid.New(), TargetID: uuid.New(), ClusterID: cluster, ProjectID: project, SourceID: source, DesiredGeneration: 1, DesiredSpecDigest: "sha256:" + strings.Repeat("a", 64), Action: "apply", Scope: string(model.ScopePlatform), SourceType: string(model.SourceHelmOCI), Url: component.Source.URL, AuthMode: string(model.AuthNone), ResolvedRevision: component.Source.Version, ArtifactDigest: component.Source.ChartDigest, Renderer: string(model.RendererHelm), DesiredOverrides: json.RawMessage(`{}`), TrustPolicy: encode(model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"}), RendererSpec: encode(model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{Chart: component.Source.Chart, ChartVersion: component.Source.Version, ReleaseName: component.ReleaseName, TargetNamespace: component.TargetNamespace, Values: encode(component.Values)}}), ReconciliationPolicy: encode(model.ReconciliationPolicy{Interval: model.Duration(time.Minute), RetryInterval: model.Duration(time.Minute), Timeout: model.Duration(5 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair})}
		queries.rows = append(queries.rows, row)
	}
	request := protocolRequest(cluster)
	response, err := state.Snapshot(t.Context(), cluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Assignments) != len(catalog.Components) {
		t.Fatal("verified keyed catalog did not reach provider")
	}
	public := snapshot.Material()
	for _, assignment := range response.Assignments {
		if assignment.Credential == nil || !bytes.Equal(assignment.Credential.Data["trust.cosign.pub"], public) || assignment.Credential.Version != response.CredentialEpoch {
			t.Fatal("captured verified key did not reach assignment")
		}
		capabilities := agentdelivery.Capabilities{SourceKinds: []protocol.DeliverySourceKind{protocol.DeliverySourceHelmOCI}, RendererKinds: []protocol.DeliveryRendererKind{protocol.DeliveryRendererHelm}, FluxAPIVersions: []string{"source.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"}, PlatformScope: true, NoCrossNamespaceRefs: true, NoRemoteKustomizeBases: true}
		materialized, err := agentdelivery.BuildAssignment(assignment, capabilities, agentdelivery.ValidationPolicy{AllowPlatformScope: true})
		if err != nil {
			t.Fatal(err)
		}
		keySeen, sourceSeen := false, false
		for _, object := range materialized.Objects {
			if object.GetKind() == "Secret" && object.GetName() == assignment.Source.TrustSecret {
				value, found, _ := unstructured.NestedFieldNoCopy(object.Object, "data", "cosign.pub")
				if !found || !bytes.Equal(value.([]byte), public) {
					t.Fatal("Flux trust Secret did not use exact captured key")
				}
				keySeen = true
			}
			if object.GetKind() == "OCIRepository" {
				name, _, _ := unstructured.NestedString(object.Object, "spec", "verify", "secretRef", "name")
				digest, _, _ := unstructured.NestedString(object.Object, "spec", "ref", "digest")
				if name != assignment.Source.TrustSecret || digest != assignment.Source.Digest {
					t.Fatal("Flux signer/digest binding changed")
				}
				sourceSeen = true
			}
		}
		if !keySeen || !sourceSeen {
			t.Fatal("keyed agent object graph missing")
		}
	}
	request.AckedSnapshotGeneration = response.SnapshotGeneration
	request.AckedETag = response.ETag
	unchanged, err := state.Snapshot(t.Context(), cluster, request)
	if err != nil || !unchanged.NotModified {
		t.Fatal("unchanged keyed snapshot lost acknowledgment")
	}
	original := queries.rows[0]
	for _, mutate := range []func(*sqlc.ListClusterDeliveryAssignmentsRow){
		func(row *sqlc.ListClusterDeliveryAssignmentsRow) { row.SourceID = uuid.New() },
		func(row *sqlc.ListClusterDeliveryAssignmentsRow) {
			row.ArtifactDigest = "sha256:" + strings.Repeat("b", 64)
		},
		func(row *sqlc.ListClusterDeliveryAssignmentsRow) {
			row.TrustPolicy = encode(model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "different"})
		},
	} {
		queries.rows[0] = original
		mutate(&queries.rows[0])
		if _, err := state.Snapshot(t.Context(), cluster, protocolRequest(cluster)); err == nil {
			t.Fatal("unowned or changed keyed assignment received public trust")
		}
	}
}
func protocolRequest(cluster uuid.UUID) protocol.DeliveryStateRequestV2 {
	return protocol.DeliveryStateRequestV2{ProtocolVersion: protocol.DeliveryProtocolVersion, ClusterID: cluster.String()}
}
