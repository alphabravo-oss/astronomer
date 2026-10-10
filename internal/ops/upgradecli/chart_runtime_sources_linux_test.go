//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const runtimeValuesFixture = `utilities:
  busybox:
    repository: fixture.invalid/utilities-busybox
    tag: exact
postgres:
  image:
    repository: fixture.invalid/postgres
    tag: exact
redis:
  image:
    repository: fixture.invalid/redis
    tag: exact
dex:
  image:
    repository: fixture.invalid/dex
    tag: exact
managementRestoreDrill:
  sidecar:
    image:
      repository: fixture.invalid/managementrestoredrill-sidecar
      tag: exact
managementLogging:
  image:
    repository: fixture.invalid/managementlogging
    tag: exact
unrelated:
  enabled: true
`

// These tests establish finite parser/source-identity behavior only. They do
// not construct authenticated chart handles or substitute for real signatures.
func TestChartRuntimeSourcesUsesExactSignedChartRolesWithoutLocalDefaults(t *testing.T) {
	manifest, wanted := canonicalHelmManifestFixture("v1.2.2")
	actual, err := chartRuntimeSources(context.Background(), []byte(runtimeValuesFixture), manifest)
	if err != nil || !reflect.DeepEqual(actual, wanted) {
		t.Fatal("exact target source role projection differs", err)
	}
	changed := strings.Replace(runtimeValuesFixture, "fixture.invalid/postgres", "unapproved.invalid/postgres", 1)
	if _, err := chartRuntimeSources(context.Background(), []byte(changed), manifest); err == nil {
		t.Fatal("caller/local PostgreSQL default bypassed signed inventory")
	}
	manifest.Astronomer.RuntimeImages = append(manifest.Astronomer.RuntimeImages, manifest.Astronomer.RuntimeImages[0])
	if _, err := chartRuntimeSources(context.Background(), []byte(runtimeValuesFixture), manifest); err == nil {
		t.Fatal("ambiguous signed source inventory admitted")
	}
}

func TestChartRuntimeSourcesRejectsAmbiguousMissingAndNonStringValues(t *testing.T) {
	manifest, _ := canonicalHelmManifestFixture("v1.2.2")
	for _, raw := range []string{
		"", "[]", runtimeValuesFixture + "---\nother: document\n",
		strings.Replace(runtimeValuesFixture, "    tag: exact", "    tag: exact\n    tag: changed", 1),
		strings.Replace(runtimeValuesFixture, "    tag: exact", "    tag: 123", 1),
		strings.Replace(runtimeValuesFixture, "    tag: exact", "    other: exact", 1),
		strings.Replace(runtimeValuesFixture, "fixture.invalid/postgres", "user:secret@fixture.invalid/postgres", 1),
		strings.Replace(runtimeValuesFixture, "    tag: exact", "    tag: exact@sha256:bad", 1),
		runtimeValuesFixture + "aliases: &source [one]\ncopy: *source\n",
	} {
		if _, err := chartRuntimeSources(context.Background(), []byte(raw), manifest); err == nil {
			t.Fatal("ambiguous runtime values admitted")
		}
	}
}

func TestChartRuntimeSourcesKeepsContextAndFiniteInputBounds(t *testing.T) {
	manifest, _ := canonicalHelmManifestFixture("v1.2.2")
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := chartRuntimeSources(ctx, []byte(runtimeValuesFixture), manifest); !errors.Is(err, context.Canceled) {
		t.Fatal("runtime source cancellation identity lost", err)
	}
	if _, err := chartRuntimeSources(nil, []byte(runtimeValuesFixture), manifest); err == nil {
		t.Fatal("nil source context admitted")
	}
	if _, err := chartRuntimeSources(context.Background(), []byte(strings.Repeat("x", (1<<20)+1)), manifest); err == nil {
		t.Fatal("runtime values byte budget ignored")
	}
}

func TestChartRuntimeSourcesSharesWholeArchiveValidationBeforePublication(t *testing.T) {
	manifest, compatibility := chartCompatibilityFixture(t)
	entries := append(normalChartEntries(compatibility), chartArchiveEntry{name: "astronomer/values.yaml", data: []byte(runtimeValuesFixture)})
	var values []byte
	actual, err := targetChartCompatibilityWithValues(context.Background(), chartArchiveFixture(t, entries), manifest, &values)
	if err != nil || string(values) != runtimeValuesFixture || string(actual) != string(compatibility) {
		t.Fatal("whole validated archive runtime source bytes lost", err)
	}
	for _, bad := range [][]chartArchiveEntry{normalChartEntries(compatibility), append(append([]chartArchiveEntry(nil), entries...), entries[len(entries)-1]), append(append([]chartArchiveEntry(nil), entries...), chartArchiveEntry{name: "astronomer/charts/foreign.yaml", data: []byte("x")})} {
		values = []byte("retained")
		if _, err := targetChartCompatibilityWithValues(context.Background(), chartArchiveFixture(t, bad), manifest, &values); err == nil || string(values) != "retained" {
			t.Fatal("failed or partial chart published runtime values")
		}
	}
}
