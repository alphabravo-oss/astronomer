package distribution

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

type blockedTransport struct{ calls *int }

func (b blockedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*b.calls++
	return nil, errors.New("verification attempted public network")
}
func TestMirrorEndorsementOfflineSignatureAndSourceBinding(t *testing.T) {
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err = os.WriteFile(filepath.Join(trust, "qa.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	catalogDigest := "sha256:" + strings.Repeat("b", 64)
	manifestDigest := "sha256:" + strings.Repeat("c", 64)
	plan := MirrorPlan{SchemaVersion: 1, Release: "v1.2.1", DistributionDigest: digest, CatalogDigest: catalogDigest, ReleaseManifestDigest: manifestDigest, Rewrites: []Rewrite{{Kind: "chart_repository", Source: "oci://vendor.example.test/charts", Target: "oci://registry.corp.local/charts"}, {Kind: "image", Source: "vendor.example.test/app@" + digest, Target: "registry.corp.local/app@" + digest}}}
	payload, _ := json.Marshal(plan)
	rawSignature, err := signer.SignMessage(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(rawSignature))
	originalTransport := http.DefaultTransport
	calls := 0
	http.DefaultTransport = blockedTransport{calls: &calls}
	defer func() { http.DefaultTransport = originalTransport }()
	accepted, err := VerifyMirrorPlan(context.Background(), payload, encoded, "qa", trust, plan.Release, digest, catalogDigest, manifestDigest)
	if err != nil || accepted.identity != contentDigest(payload) || calls != 0 {
		t.Fatalf("endorsement err=%v network=%d", err, calls)
	}
	for _, test := range []struct {
		name               string
		payload, signature []byte
		release, manifest  string
	}{
		{"tamper", append(payload, []byte(" ")...), encoded, plan.Release, manifestDigest},
		{"wrong signature", payload, []byte("invalid"), plan.Release, manifestDigest},
		{"wrong release", payload, encoded, "v1.2.2", manifestDigest},
		{"wrong manifest", payload, encoded, plan.Release, digest},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyMirrorPlan(context.Background(), test.payload, test.signature, "qa", trust, test.release, digest, catalogDigest, test.manifest); err == nil {
				t.Fatal("invalid endorsement accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatal("offline endorsement fetched external trust")
	}
}
