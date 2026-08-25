package builtin

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
)

func TestStableIDIsDeterministicAndDomainSeparated(t *testing.T) {
	first := stableID("target", "cluster-a", "kube-state-metrics")
	if first != stableID("target", "cluster-a", "kube-state-metrics") {
		t.Fatal("stable ID changed for identical identity parts")
	}
	if first == stableID("bundle", "cluster-a", "kube-state-metrics") {
		t.Fatal("stable ID did not separate resource domains")
	}
}

func TestPlanCatalogSourcesReusesCurrentSourceAndPreservesIdentity(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	projectID := uuid.MustParse("cce9f188-cc1a-4e32-a621-cb14db221f12")
	sources, byURL, err := planCatalogSources(catalog, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || len(byURL) != 1 {
		t.Fatalf("current catalog planned %d sources / %d URL bindings, want one", len(sources), len(byURL))
	}
	want := sourceIdentity{
		id: stableID("source", projectID.String(), systemSourceName), name: systemSourceName, url: systemSourceURL,
	}
	if sources[0] != want || byURL[systemSourceURL] != want {
		t.Fatalf("current source identity = %+v / %+v, want %+v", sources[0], byURL[systemSourceURL], want)
	}
}

func TestPlanCatalogSourcesBindsMultipleSourcesDeterministically(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	secondURL := "https://charts.example.test/stable"
	catalog.Components[1].Source.URL = secondURL
	projectID := uuid.MustParse("ad27b142-eab5-44b9-8ec5-4042b331e916")
	first, firstByURL, err := planCatalogSources(catalog, projectID)
	if err != nil {
		t.Fatal(err)
	}
	second, secondByURL, err := planCatalogSources(catalog, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstByURL, secondByURL) {
		t.Fatal("multi-source planning changed across identical retries")
	}
	if len(first) != 2 || first[0].url != secondURL || first[1].url != systemSourceURL {
		t.Fatalf("multi-source plan is not URL-sorted and complete: %+v", first)
	}
	if firstByURL[secondURL].id == firstByURL[systemSourceURL].id || firstByURL[secondURL].name == firstByURL[systemSourceURL].name {
		t.Fatalf("distinct source URLs share an identity: %+v", firstByURL)
	}
	for _, component := range catalog.Components {
		if source := firstByURL[component.Source.URL]; source.url != component.Source.URL {
			t.Fatalf("component %s bound to %+v", component.Slug, source)
		}
	}
}

func TestPlanCatalogSourcesRejectsAmbiguousNormalization(t *testing.T) {
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Components[1].Source.URL += "/"
	if _, _, err := planCatalogSources(catalog, uuid.New()); err == nil {
		t.Fatal("ambiguous source URL reached provisioning")
	}
}

func TestRetryableTransactionError(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		if !retryableTransactionError(&pgconn.PgError{Code: code}) {
			t.Fatalf("PostgreSQL transaction error %s is not retryable", code)
		}
	}
	if retryableTransactionError(&pgconn.PgError{Code: "23505"}) || retryableTransactionError(errors.New("40001")) {
		t.Fatal("non-transaction error was marked retryable")
	}
}

func TestRetryAfterRequiresNewerExplicitRequest(t *testing.T) {
	created := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	before, equal, after := created.Add(-time.Second), created, created.Add(time.Second)
	for name, tc := range map[string]struct {
		requested *time.Time
		want      bool
	}{
		"none":   {requested: nil, want: false},
		"before": {requested: &before, want: false},
		"equal":  {requested: &equal, want: false},
		"after":  {requested: &after, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := retryAfter(tc.requested, created); got != tc.want {
				t.Fatalf("retryAfter() = %v, want %v", got, tc.want)
			}
		})
	}
}
