package distribution

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

func databaseRepositoryMirrorFixture(t *testing.T, rewrites []Rewrite) (*MirrorPlan, DatabaseUpdateBinding) {
	t.Helper()
	binding := databaseUpdateFixture(time.Now().UTC()).Binding
	signer, key, err := signature.NewDefaultECDSASignerVerifier()
	if err != nil {
		t.Fatal(err)
	}
	public, err := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, "operator.pub"), public, 0600); err != nil {
		t.Fatal(err)
	}
	document := MirrorPlan{SchemaVersion: 1, Release: binding.BaseRelease, DistributionDigest: binding.BaseDistributionDigest, CatalogDigest: binding.CatalogDigest, ReleaseManifestDigest: "sha256:" + strings.Repeat("c", 64), Rewrites: rewrites}
	value, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.SignMessage(bytes.NewReader(value))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := VerifyMirrorPlan(t.Context(), value, []byte(base64.StdEncoding.EncodeToString(signed)), "operator", trust, document.Release, document.DistributionDigest, document.CatalogDigest, document.ReleaseManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	binding.MirrorEndorsementDigest = plan.Identity()
	return plan, binding
}

func TestDatabaseRepositoryMirrorRequiresVerifiedExactBaseAndUnchangedSubject(t *testing.T) {
	source, target := "publisher.example.test/database", "mirror.example.test/database"
	plan, binding := databaseRepositoryMirrorFixture(t, []Rewrite{{Kind: "database_repository", Source: source, Target: target}})
	for _, hex := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		subject := "sha256:" + hex
		actual, err := databaseMirrorSubject(plan, binding, source+"@"+subject)
		if err != nil || actual != target+"@"+subject {
			t.Fatal("verified repository mapping changed or lost child digest")
		}
	}
	subject := source + "@sha256:" + strings.Repeat("a", 64)
	for _, scenario := range []string{"unsigned", "wrong-mirror", "wrong-base", "mutable", "changed-document"} {
		t.Run(scenario, func(t *testing.T) {
			selected := plan
			expected := binding
			original := subject
			switch scenario {
			case "unsigned":
				copy := mirrorDocument(*plan)
				selected = &copy
			case "wrong-mirror":
				expected.MirrorEndorsementDigest = "sha256:" + strings.Repeat("d", 64)
			case "wrong-base":
				expected.BaseDistributionDigest = "sha256:" + strings.Repeat("d", 64)
			case "mutable":
				original = source + ":current"
			case "changed-document":
				copy := *plan
				copy.Rewrites = append([]Rewrite(nil), plan.Rewrites...)
				copy.Rewrites[0].Target = "changed.example.test/database"
				selected = &copy
			}
			if _, err := databaseMirrorSubject(selected, expected, original); err == nil {
				t.Fatal("unverified or changed repository mapping granted destination")
			}
		})
	}
	if _, err := databaseMirrorSubject(nil, binding, subject); err == nil {
		t.Fatal("missing bound endorsement accepted")
	}
	binding.MirrorEndorsementDigest = ""
	if actual, err := databaseMirrorSubject(nil, binding, subject); err != nil || actual != subject {
		t.Fatal("ordinary unmirrored immutable subject changed")
	}
}

func TestDatabaseRepositoryMirrorRejectsDuplicateCollisionChainAndExactDisagreement(t *testing.T) {
	for _, rewrites := range [][]Rewrite{
		{{Kind: "database_repository", Source: "vendor.test/a", Target: "mirror.test/a"}, {Kind: "database_repository", Source: "vendor.test/a", Target: "mirror.test/b"}},
		{{Kind: "database_repository", Source: "vendor.test/a", Target: "mirror.test/a"}, {Kind: "database_repository", Source: "vendor.test/b", Target: "mirror.test/a"}},
		{{Kind: "database_repository", Source: "vendor.test/a", Target: "mirror.test/a"}, {Kind: "database_repository", Source: "mirror.test/a", Target: "other.test/a"}},
		{{Kind: "database_repository", Source: "vendor.test/a", Target: "vendor.test/a"}},
		{{Kind: "database_repository", Source: "vendor.test/a", Target: "mirror.test/a"}, {Kind: "artifact", Source: "vendor.test/a@sha256:" + strings.Repeat("a", 64), Target: "changed.test/a@sha256:" + strings.Repeat("a", 64)}},
	} {
		if err := validateDatabaseMirrorRepositories(rewrites); err == nil {
			t.Fatal("ambiguous or chained repository mapping accepted")
		}
	}
}
