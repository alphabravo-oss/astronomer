package provider

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func fixtureCA(t *testing.T) []byte {
	return fixtureCAWithName(t, "owned fixture CA")
}

func fixtureCAWithName(t *testing.T, name string) []byte {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
func builtinOCIRow(t *testing.T) sqlc.ListClusterDeliveryAssignmentsRow {
	row := gitRow(t)
	row.Scope = string(model.ScopePlatform)
	row.SourceType = string(model.SourceHelmOCI)
	row.Url = "oci://10.20.30.40/builtins/charts"
	row.AuthMode = string(model.AuthNone)
	row.CredentialEncrypted = ""
	row.CaBundleEncrypted = ""
	row.CredentialEpoch = 0
	row.ResolvedRevision = "1.0.0"
	row.Renderer = string(model.RendererHelm)
	row.RendererSpec = mustJSON(t, model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{Chart: "metrics", ChartVersion: "1.0.0", ReleaseName: "metrics", TargetNamespace: "metrics"}})
	row.TrustPolicy = mustJSON(t, model.TrustPolicy{Provider: model.SignatureCosignKeyless, Identity: "publisher", Issuer: "issuer"})
	return row
}
func TestBuiltinCARotationAndActualFluxMaterialization(t *testing.T) {
	ctx := context.Background()
	row := builtinOCIRow(t)
	queries := &fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}
	authorize := func(_ context.Context, cluster, project, source uuid.UUID, assignment protocol.DeliveryAssignmentV2) (bool, error) {
		return cluster == testCluster && project == testProject && source == testSource && assignment.Source.Digest == row.ArtifactDigest, nil
	}
	ca := fixtureCA(t)
	original := bytes.Clone(ca)
	p, err := NewWithBuiltinCA(queries, nil, ca, authorize)
	if err != nil {
		t.Fatal(err)
	}
	clear(ca)
	first, err := p.Snapshot(ctx, testCluster, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	assignment := first.Assignments[0]
	if !bytes.Equal(assignment.Credential.Data["caFile"], original) || assignment.Credential.Version != first.CredentialEpoch {
		t.Fatal("CA snapshot/version was not captured")
	}
	capabilities := agentdelivery.Capabilities{SourceKinds: []protocol.DeliverySourceKind{protocol.DeliverySourceHelmOCI}, RendererKinds: []protocol.DeliveryRendererKind{protocol.DeliveryRendererHelm}, FluxAPIVersions: []string{"source.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"}, PlatformScope: true, NoCrossNamespaceRefs: true, NoRemoteKustomizeBases: true}
	materialized, err := agentdelivery.BuildAssignment(assignment, capabilities, agentdelivery.ValidationPolicy{AllowPlatformScope: true})
	if err != nil {
		t.Fatal(err)
	}
	secretSeen, sourceSeen := false, false
	for _, object := range materialized.Objects {
		if object.GetKind() == "Secret" {
			value, found, _ := unstructured.NestedFieldNoCopy(object.Object, "data", "ca.crt")
			if found && bytes.Equal(value.([]byte), original) {
				secretSeen = true
			}
			if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "data", "caFile"); found {
				t.Fatal("deprecated CA key emitted")
			}
		}
		if object.GetKind() == "OCIRepository" {
			name, _, _ := unstructured.NestedString(object.Object, "spec", "certSecretRef", "name")
			if name != assignment.Source.CredentialSecret {
				t.Fatal("OCI TLS ref mismatch")
			}
			if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", "secretRef"); found {
				t.Fatal("anonymous CA-only source mislabeled registry authentication")
			}
			digest, _, _ := unstructured.NestedString(object.Object, "spec", "ref", "digest")
			if digest != row.ArtifactDigest {
				t.Fatal("immutable digest changed")
			}
			if _, found, _ := unstructured.NestedFieldNoCopy(object.Object, "spec", "verify"); !found {
				t.Fatal("vendor verification removed")
			}
			sourceSeen = true
		}
	}
	if !secretSeen || !sourceSeen {
		t.Fatal("actual Secret/OCI graph missing")
	}
	request := validRequest()
	request.AckedSnapshotGeneration = first.SnapshotGeneration
	request.AckedETag = first.ETag
	unchanged, err := p.Snapshot(ctx, testCluster, request)
	if err != nil || !unchanged.NotModified {
		t.Fatal("unchanged CA lost ack behavior")
	}
	rotated, err := NewWithBuiltinCA(queries, nil, fixtureCA(t), authorize)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rotated.Snapshot(ctx, testCluster, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.NotModified || second.CredentialEpoch <= first.CredentialEpoch || second.ETag == first.ETag || second.Assignments[0].Credential.Version != second.CredentialEpoch {
		t.Fatal("CA rotation did not invalidate credential identity")
	}
	clear(second.Assignments[0].Credential.Data["caFile"])
	again, err := rotated.Snapshot(ctx, testCluster, validRequest())
	if err != nil || len(again.Assignments[0].Credential.Data["caFile"]) == 0 {
		t.Fatal("response mutation changed retained CA")
	}
}
func TestBuiltinCADenialsAndParseBoundary(t *testing.T) {
	for _, ca := range [][]byte{nil, []byte("invalid"), []byte(strings.Repeat("x", (1<<20)+1)), append(fixtureCA(t), []byte("garbage")...)} {
		if _, err := NewWithBuiltinCA(&fakeQueries{}, nil, ca, func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, protocol.DeliveryAssignmentV2) (bool, error) {
			return true, nil
		}); err == nil {
			t.Fatal("invalid CA accepted")
		}
	}
	row := builtinOCIRow(t)
	for _, fail := range []bool{false, true} {
		p, err := NewWithBuiltinCA(&fakeQueries{rows: []sqlc.ListClusterDeliveryAssignmentsRow{row}}, nil, fixtureCA(t), func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, protocol.DeliveryAssignmentV2) (bool, error) {
			if fail {
				return false, errors.New("denied")
			}
			return false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := p.Snapshot(context.Background(), testCluster, validRequest())
		if fail {
			if err == nil {
				t.Fatal("authorization failure ignored")
			}
			continue
		}
		if err != nil || response.Assignments[0].Credential != nil || response.Assignments[0].Source.CredentialSecret != "" {
			t.Fatal("unapproved source received CA")
		}
	}
}

func TestBuiltinCAHonorsProtocolValueLimit(t *testing.T) {
	ca := fixtureCAWithName(t, strings.Repeat("x", protocol.MaxDeliveryCredentialValue))
	if len(ca) <= protocol.MaxDeliveryCredentialValue || len(ca) > 1<<20 {
		t.Fatal("oversized valid certificate fixture missed intended boundary")
	}
	authorize := func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, protocol.DeliveryAssignmentV2) (bool, error) {
		return true, nil
	}
	if _, err := NewWithBuiltinCA(&fakeQueries{}, nil, ca, authorize); err == nil {
		t.Fatal("CA exceeding protocol credential value bound accepted at construction")
	}
}
