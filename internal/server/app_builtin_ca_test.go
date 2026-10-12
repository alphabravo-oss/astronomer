package server

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	bundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

type caCatalogFixture struct {
	catalog bundles.Catalog
	err     error
	calls   int
}

func (f *caCatalogFixture) Load(context.Context) (bundles.Catalog, error) {
	f.calls++
	return f.catalog, f.err
}
func TestBuiltinCAAuthorizationBindsCatalogIdentityAndDestination(t *testing.T) {
	catalog, err := bundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog.DistributionIdentity = "sha256:" + strings.Repeat("a", 64)
	catalog.VerificationProvider = "cosign_keyless"
	catalog.VerificationIdentity = "publisher"
	catalog.VerificationIssuer = "issuer"
	component := catalog.Components[0]
	component.Source.OriginalURL = component.Source.URL
	component.Source.URL = "oci://10.20.30.40/charts"
	component.Source.Kind = "helm_oci"
	catalog.Components = []bundles.Component{component}
	cluster := uuid.New()
	ns := uuid.MustParse("2b079af7-4a47-4dbe-984b-a67d78ab6909")
	project := uuid.NewSHA1(ns, []byte("project\x00"+cluster.String()))
	source := uuid.NewSHA1(ns, []byte("source\x00"+project.String()+"\x00"+component.Source.URL+"\x00"+catalog.DistributionIdentity))
	assignment := protocol.DeliveryAssignmentV2{ProjectID: project.String(), Scope: protocol.DeliveryScopePlatform, Source: protocol.DeliverySourceV2{Kind: protocol.DeliverySourceHelmOCI, URL: component.Source.URL, Chart: component.Source.Chart, Revision: component.Source.Version, Digest: component.Source.ChartDigest, Verify: &protocol.DeliveryVerifyV2{Provider: "cosign", OIDCIdentities: []protocol.DeliveryOIDCIdentity{{Issuer: "issuer", Subject: "publisher"}}}}}
	policy := httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: "https://10.20.30.40", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.30.40/32")}}}}
	fixture := &caCatalogFixture{catalog: catalog}
	authorize := builtinCAAuthorizer(fixture, policy)
	policy.Origins[0].URL = "https://10.20.30.41"
	approved, err := authorize(context.Background(), cluster, project, source, assignment)
	if err != nil || !approved || fixture.calls != 1 {
		t.Fatalf("exact catalog/destination authorization failed: %v", err)
	}
	for _, mutate := range []func(*protocol.DeliveryAssignmentV2){
		func(a *protocol.DeliveryAssignmentV2) { a.Source.Digest = "sha256:" + strings.Repeat("b", 64) },
		func(a *protocol.DeliveryAssignmentV2) { a.Source.URL = "oci://10.20.30.41/charts" },
		func(a *protocol.DeliveryAssignmentV2) { a.Source.Chart = "other" },
		func(a *protocol.DeliveryAssignmentV2) { a.Source.Revision = "wrong" },
		func(a *protocol.DeliveryAssignmentV2) { a.Source.Kind = protocol.DeliverySourceHelmHTTP },
		func(a *protocol.DeliveryAssignmentV2) { a.Scope = protocol.DeliveryScopeNamespace },
		func(a *protocol.DeliveryAssignmentV2) { a.Source.Verify = nil },
		func(a *protocol.DeliveryAssignmentV2) {
			a.Source.Verify = &protocol.DeliveryVerifyV2{Provider: "cosign", OIDCIdentities: []protocol.DeliveryOIDCIdentity{{Issuer: "issuer", Subject: "wrong"}}}
		},
	} {
		changed := assignment
		mutate(&changed)
		if approved, err := authorize(context.Background(), cluster, project, source, changed); err != nil || approved {
			t.Fatal("changed assignment received builtin CA")
		}
	}
	for _, ids := range [][3]uuid.UUID{{uuid.New(), project, source}, {cluster, uuid.New(), source}, {cluster, project, uuid.New()}} {
		if approved, err := authorize(context.Background(), ids[0], ids[1], ids[2], assignment); err != nil || approved {
			t.Fatal("nonbuiltin ownership received CA")
		}
	}
	unapproved := builtinCAAuthorizer(fixture, httpclient.DestinationPolicy{})
	if approved, err := unapproved(context.Background(), cluster, project, source, assignment); err == nil || approved {
		t.Fatal("private destination without authority accepted")
	}
	outside := builtinCAAuthorizer(fixture, httpclient.DestinationPolicy{Restricted: true, Origins: []httpclient.TrustedOrigin{{URL: "https://10.20.30.40", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.30.41/32")}}}})
	if approved, err := outside(context.Background(), cluster, project, source, assignment); err == nil || approved {
		t.Fatal("approved origin outside CIDR accepted")
	}
	fixture.err = errors.New("signature verification failed")
	if approved, err := authorize(context.Background(), cluster, project, source, assignment); err == nil || approved {
		t.Fatal("unverified/unavailable catalog accepted")
	}
	fixture.err = nil
	fixture.catalog.DistributionIdentity = ""
	if approved, err := authorize(context.Background(), cluster, project, source, assignment); err != nil || approved {
		t.Fatal("source development received vendor CA qualification")
	}
}

func TestBuiltinCAFileBoundaries(t *testing.T) {
	directory := t.TempDir()
	if _, err := readBuiltinCA(directory); err == nil {
		t.Fatal("directory accepted as CA")
	}
	if _, err := readBuiltinCA(directory + "/missing"); err == nil {
		t.Fatal("missing CA accepted")
	}
	filename := directory + "/ca.pem"
	for _, data := range [][]byte{nil, make([]byte, (1<<20)+1)} {
		if err := os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readBuiltinCA(filename); err == nil {
			t.Fatal("empty/oversized CA read accepted")
		}
	}
	if ca, err := readBuiltinCA(""); err != nil || len(ca) != 0 {
		t.Fatal("absent optional CA changed default")
	}
}
