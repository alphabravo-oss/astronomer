package builtin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/google/uuid"
)

// These queries capture the actual ordinary persistence boundary. A before
// characterization replaces only provisioner.go with its unchanged production
// bytes while retaining the proposed pure compiler and tests. No DB is mocked
// as transactional authority, and these captures prove no durable acceptance.
type componentCompilerQueries struct {
	calls                      []string
	bundle                     sqlc.EnsureBuiltinComponentBundleParams
	version                    sqlc.EnsureBuiltinComponentVersionParams
	target                     sqlc.EnsureBuiltinComponentTargetParams
	wrongProject, wrongVersion bool
}

func (q *componentCompilerQueries) EnsureBuiltinComponentBundle(ctx context.Context, value sqlc.EnsureBuiltinComponentBundleParams) error {
	q.calls = append(q.calls, "bundle")
	q.bundle = value
	return ctx.Err()
}
func (q *componentCompilerQueries) GetBuiltinComponentBundleProject(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	q.calls = append(q.calls, "bundle-project")
	if q.wrongProject {
		return uuid.Nil, nil
	}
	return q.bundle.ProjectID, ctx.Err()
}
func (q *componentCompilerQueries) EnsureBuiltinComponentVersion(ctx context.Context, value sqlc.EnsureBuiltinComponentVersionParams) error {
	q.calls = append(q.calls, "version")
	q.version = value
	return ctx.Err()
}
func (q *componentCompilerQueries) GetBuiltinComponentVersionIdentity(ctx context.Context, id uuid.UUID) (sqlc.GetBuiltinComponentVersionIdentityRow, error) {
	q.calls = append(q.calls, "version-identity")
	digest := q.version.SpecDigest
	if q.wrongVersion {
		digest = "sha256:" + strings.Repeat("0", 64)
	}
	return sqlc.GetBuiltinComponentVersionIdentityRow{BundleID: q.version.BundleID, SpecDigest: digest, State: "ready"}, ctx.Err()
}
func (q *componentCompilerQueries) EnsureBuiltinComponentTarget(ctx context.Context, value sqlc.EnsureBuiltinComponentTargetParams) (int64, error) {
	q.calls = append(q.calls, "target")
	q.target = value
	return 7, ctx.Err()
}

func compilerCatalogFixture(t *testing.T, identity string) builtinbundles.Catalog {
	t.Helper()
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	if identity == "development" {
		return catalog
	}
	// Synthetic pure compiler INPUT DATA. These strings are not a signed catalog
	// proof, production OIDC identity or optional durable repository authority.
	catalog.DistributionIdentity = "sha256:" + strings.Repeat("a", 64)
	if identity == "mirrored" {
		catalog.DistributionIdentity += ";mirror:sha256:" + strings.Repeat("b", 64)
	}
	catalog.VerificationProvider = string(model.SignatureCosignKey)
	catalog.VerificationKeyRef = "operator-qa"
	for index := range catalog.Components {
		component := &catalog.Components[index]
		component.Source.OriginalURL = component.Source.URL
		component.Source.OriginalArchiveDigest = component.Source.ChartDigest
		component.Source.Kind = "helm_oci"
		component.Source.URL = "oci://registry.example.test/charts"
		for i, capability := range component.Requirements.Capabilities {
			if capability == "delivery.source.helm_http" {
				component.Requirements.Capabilities[i] = "delivery.source.helm_oci"
			}
		}
	}
	return catalog
}

func TestBuiltinComponentCompilerMatchesOrdinaryPersistenceAcrossIdentities(t *testing.T) {
	cluster := uuid.MustParse("4ea50c0a-7b5a-4166-ab15-0da4de95f0dc")
	for _, identity := range []string{"development", "verified", "mirrored"} {
		for _, slug := range []string{"trivy-operator", "kube-state-metrics", "prometheus-node-exporter"} {
			t.Run(identity+"/"+slug, func(t *testing.T) {
				catalog := compilerCatalogFixture(t, identity)
				expected, err := ExpectedCatalogComponent(catalog, cluster, slug)
				if err != nil {
					t.Fatal(err)
				}
				var component builtinbundles.Component
				for _, candidate := range catalog.Components {
					if candidate.Slug == slug {
						component = candidate
					}
				}
				source, err := catalogSourceIdentityForComponent(expected.ProjectID, component, catalog.DistributionIdentity)
				if err != nil {
					t.Fatal(err)
				}
				queries := &componentCompilerQueries{}
				target, err := ensureComponent(t.Context(), queries, expected.ProjectID, cluster, source, catalog.Release, component, catalogTrustPolicy(catalog), catalog.DistributionIdentity, catalog.VerificationIdentity)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(queries.version, expected.Version) || target.id != expected.TargetID || target.generation != 7 || queries.target.BundleVersionID != expected.VersionID || string(queries.target.ReconciliationPolicy) != string(expected.Version.ReconciliationPolicy) {
					t.Fatal("shared compiler changed actual ordinary persisted source/renderer/spec/target")
				}
				if !reflect.DeepEqual(queries.calls, []string{"bundle", "bundle-project", "version", "version-identity", "target"}) {
					t.Fatal("ordinary persistence order changed")
				}
				if expected.SourceID != source.id || expected.Version.SourceID != source.id || expected.BundleID != queries.bundle.ID || expected.Version.BundleID != queries.bundle.ID {
					t.Fatal("stable source/bundle identity changed")
				}
				repeated, err := ExpectedCatalogComponent(catalog, cluster, slug)
				if err != nil || !reflect.DeepEqual(repeated, expected) {
					t.Fatal("compiler outputs are not deterministic")
				}
			})
		}
	}
}

func TestBuiltinComponentCompilerBindsDesiredValuesAndReturnedBytesAreData(t *testing.T) {
	cluster := uuid.MustParse("97777db6-a2dc-498c-9dcd-62b3de5ea6cb")
	catalog := compilerCatalogFixture(t, "mirrored")
	original, err := ExpectedCatalogComponent(catalog, cluster, "trivy-operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"scanner-image", "namespace", "source", "chart-version", "chart-digest"} {
		t.Run(change, func(t *testing.T) {
			altered := compilerCatalogFixture(t, "mirrored")
			component := &altered.Components[0]
			switch change {
			case "scanner-image":
				component.Values["trivy"].(map[string]any)["image"].(map[string]any)["tag"] = "0.75.0@sha256:" + strings.Repeat("c", 64)
			case "namespace":
				component.TargetNamespace = "different-namespace"
			case "source":
				component.Source.URL = "oci://other.example.test/charts"
			case "chart-version":
				component.Source.Version = "9.9.9"
			case "chart-digest":
				component.Source.ChartDigest = "sha256:" + strings.Repeat("c", 64)
			}
			changed, err := ExpectedCatalogComponent(altered, cluster, "trivy-operator")
			if err != nil {
				t.Fatal(err)
			}
			if changed.Version.SpecDigest == original.Version.SpecDigest {
				t.Fatal("changed desired scanner/source/renderer retained old canonical spec")
			}
			if changed.TargetID != original.TargetID {
				t.Fatal("desired change replaced stable target ownership")
			}
		})
	}
	// Editing returned serialized DATA is never an edit to the input catalog,
	// an immutable DB row, or permission to commit a different desired state.
	original.Version.RendererSpec[0] = '!'
	original.Version.SourceSpec[0] = '!'
	repeated, err := ExpectedCatalogComponent(catalog, cluster, "trivy-operator")
	if err != nil || repeated.Version.RendererSpec[0] == '!' || repeated.Version.SourceSpec[0] == '!' {
		t.Fatal("caller modified future compiler output through shared bytes")
	}
	for _, invalid := range []string{"nil-cluster", "missing-component"} {
		selected, slug := cluster, "trivy-operator"
		if invalid == "nil-cluster" {
			selected = uuid.Nil
		} else {
			slug = "not-reviewed"
		}
		if _, err := ExpectedCatalogComponent(catalog, selected, slug); err == nil {
			t.Fatal("incomplete expectation compiled")
		}
	}
}

func TestBuiltinComponentCompilerPreservesOrdinaryFailureOrderAndCancellation(t *testing.T) {
	catalog := compilerCatalogFixture(t, "development")
	cluster := uuid.MustParse("12f76e32-0e26-42d3-9b89-ac7db05d2c88")
	project := stableID("project", cluster.String())
	for _, failure := range []string{"bundle-project", "unencodable-values", "invalid-digest", "version-conflict", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			component := catalog.Components[0]
			queries := &componentCompilerQueries{}
			ctx := t.Context()
			switch failure {
			case "bundle-project":
				queries.wrongProject = true
			case "unencodable-values":
				component.Values = map[string]any{"unsupported": func() {}}
			case "invalid-digest":
				component.Source.ChartDigest = "invalid"
			case "version-conflict":
				queries.wrongVersion = true
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			source, err := catalogSourceIdentityForComponent(project, component, catalog.DistributionIdentity)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ensureComponent(ctx, queries, project, cluster, source, catalog.Release, component, catalogTrustPolicy(catalog), catalog.DistributionIdentity, catalog.VerificationIdentity)
			if err == nil {
				t.Fatal("ordinary persistence ignored failure")
			}
			expected := []string{"bundle", "bundle-project"}
			if failure == "version-conflict" {
				expected = append(expected, "version", "version-identity")
			}
			if failure == "canceled" {
				expected = []string{"bundle"}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation was lost")
				}
			}
			if !reflect.DeepEqual(queries.calls, expected) {
				t.Fatal("pure extraction changed write/error ordering")
			}
		})
	}
}
