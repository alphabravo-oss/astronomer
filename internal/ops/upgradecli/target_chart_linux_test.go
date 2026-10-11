//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// These parser/private-file fixtures do not substitute for a successful OCI or
// signature gate. No synthetic verifier result can construct TargetChartContent.
func chartCompatibilityFixture(t *testing.T) (releasecontract.Manifest, []byte) {
	t.Helper()
	m, _ := canonicalHelmManifestFixture("v1.2.2")
	c := chartCompatibility{SchemaVersion: 1, ReleaseLine: "1.2.x"}
	c.Agent.FutureMajorPolicy = "reject"
	c.Agent.SupportWindow = 2
	c.Agent.Heartbeat.Minimum = m.Compatibility.Agent.HeartbeatSchemaMinimum
	c.Agent.Heartbeat.Maximum = m.Compatibility.Agent.HeartbeatSchemaMaximum
	c.Agent.Tunnel.Minimum = m.Compatibility.Agent.TunnelProtocolMinimum
	c.Agent.Tunnel.Maximum = m.Compatibility.Agent.TunnelProtocolMaximum
	c.Agent.MinimumCompatibleVersion = m.Compatibility.Agent.MinimumCompatibleVersion
	c.Agent.MinimumSupportedVersion = m.Compatibility.Agent.MinimumSupportedVersion
	c.Agent.MaximumSupportedVersionExclusive = m.Compatibility.Agent.MaximumSupportedVersionExclusive
	c.Protocol.Name = m.Compatibility.AgentProtocol.Name
	c.Protocol.Capabilities = append([]string(nil), m.Compatibility.AgentProtocol.RequiredCapabilities...)
	c.Protocol.UnknownPolicy = "reject"
	c.Protocol.Ranges = append(c.Protocol.Ranges, chartRange{m.Compatibility.AgentProtocol.Minimum, m.Compatibility.AgentProtocol.Maximum})
	c.PostgreSQL.MigrationMode = "versioned_incremental"
	c.PostgreSQL.TLSRequired = true
	c.PostgreSQL.Minimum = int64(m.Compatibility.PostgreSQL.MinimumUpgradeSchema)
	c.PostgreSQL.Target = int64(m.Compatibility.PostgreSQL.TargetSchema)
	c.PostgreSQL.Reversible = int64(m.Compatibility.PostgreSQL.ReversibleThroughSchema)
	c.PostgreSQL.UpgradeLines = []string{"1.1.x", "1.2.x"}
	for _, v := range m.Compatibility.PostgreSQL.SupportedMajors {
		c.PostgreSQL.Majors = append(c.PostgreSQL.Majors, uint32(v))
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return m, raw
}

type chartArchiveEntry struct {
	name string
	data []byte
	kind byte
	link string
}

func chartArchiveFixture(t *testing.T, entries []chartArchiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: kind, Linkname: entry.link}
		if kind == tar.TypeDir || kind == tar.TypeSymlink {
			h.Size = 0
		}
		if err := archive.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := archive.Write(entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func normalChartEntries(compatibility []byte) []chartArchiveEntry {
	return []chartArchiveEntry{{name: "astronomer/Chart.yaml", data: []byte("apiVersion: v2\nname: astronomer\nversion: 1.2.2\nappVersion: v1.2.2\ntype: application\n")}, {name: "astronomer/files/release-compatibility.json", data: compatibility}, {name: "astronomer/templates/server.yaml", data: []byte("kind: Deployment\n")}}
}
func TestTargetChartReadsBoundedExactArchiveCompatibilityWithoutExtraction(t *testing.T) {
	m, compatibility := chartCompatibilityFixture(t)
	raw := chartArchiveFixture(t, normalChartEntries(compatibility))
	actual, err := targetChartCompatibility(context.Background(), raw, m)
	if err != nil || !bytes.Equal(actual, compatibility) {
		t.Fatal("exact chart compatibility bytes lost", err)
	}
	bounds, err := compareChartCompatibility(actual, m)
	if err != nil || bounds.Minimum != uint32(m.Compatibility.PostgreSQL.MinimumUpgradeSchema) || bounds.Target != uint32(m.Compatibility.PostgreSQL.TargetSchema) {
		t.Fatal("signed schema bounds differ", err)
	}
	if _, err := targetChartCompatibility(nil, raw, m); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil archive context admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := targetChartCompatibility(ctx, raw, m); !errors.Is(err, context.Canceled) {
		t.Fatal("archive cancellation identity lost", err)
	}
}
func TestTargetChartRejectsAmbiguousArchivePathsDependenciesAndCompressedTails(t *testing.T) {
	m, compatibility := chartCompatibilityFixture(t)
	for _, mode := range []string{"duplicate", "traversal", "absolute", "link", "nested-dependency", "foreign-root", "wrong-version", "two-yaml-documents", "yaml-duplicate", "missing-compatibility", "concatenated-gzip", "compressed-trailing", "corrupt-footer", "too-many-entries"} {
		t.Run(mode, func(t *testing.T) {
			entries := normalChartEntries(compatibility)
			switch mode {
			case "duplicate":
				entries = append(entries, entries[0])
			case "traversal":
				entries = append(entries, chartArchiveEntry{name: "astronomer/../outside", data: []byte("x")})
			case "absolute":
				entries = append(entries, chartArchiveEntry{name: "/astronomer/outside", data: []byte("x")})
			case "link":
				entries = append(entries, chartArchiveEntry{name: "astronomer/link", kind: tar.TypeSymlink, link: "outside"})
			case "nested-dependency":
				entries = append(entries, chartArchiveEntry{name: "astronomer/charts/dependency.tgz", data: []byte("x")})
			case "foreign-root":
				entries = append(entries, chartArchiveEntry{name: "other/files/release-compatibility.json", data: compatibility})
			case "wrong-version":
				entries[0].data = []byte("apiVersion: v2\nname: astronomer\nversion: 1.2.20\nappVersion: v1.2.2\n")
			case "two-yaml-documents":
				entries[0].data = append(entries[0].data, []byte("---\nname: second\n")...)
			case "yaml-duplicate":
				entries[0].data = append(entries[0].data, []byte("name: astronomer\n")...)
			case "missing-compatibility":
				entries = append(entries[:1], entries[2:]...)
			case "too-many-entries":
				for i := 0; i < targetChartEntriesMaximum; i++ {
					entries = append(entries, chartArchiveEntry{name: fmt.Sprintf("astronomer/entry-%04d", i), data: []byte("x")})
				}
			}
			raw := chartArchiveFixture(t, entries)
			switch mode {
			case "concatenated-gzip":
				raw = append(raw, chartArchiveFixture(t, normalChartEntries(compatibility))...)
			case "compressed-trailing":
				raw = append(raw, []byte("unowned tail")...)
			case "corrupt-footer":
				raw[len(raw)-8] ^= 1
			}
			if _, err := targetChartCompatibility(context.Background(), raw, m); err == nil {
				t.Fatal("ambiguous chart archive admitted")
			}
		})
	}
}
func TestTargetChartCompatibilityRejectsUnsignedSchemaOrAgentDifferences(t *testing.T) {
	m, raw := chartCompatibilityFixture(t)
	for _, mode := range []string{"unknown", "duplicate", "trailing", "minimum", "target", "reversible", "major", "agent", "line"} {
		t.Run(mode, func(t *testing.T) {
			changed := append([]byte(nil), raw...)
			switch mode {
			case "unknown":
				changed = append([]byte(`{"unknown":true,`), raw[1:]...)
			case "duplicate":
				changed = append([]byte(`{"schema_version":1,`), raw[1:]...)
			case "trailing":
				changed = append(changed, []byte(` {}`)...)
			default:
				var c chartCompatibility
				if json.Unmarshal(raw, &c) != nil {
					t.Fatal("fixture decode")
				}
				switch mode {
				case "minimum":
					c.PostgreSQL.Minimum++
				case "target":
					c.PostgreSQL.Target++
				case "reversible":
					c.PostgreSQL.Reversible++
				case "major":
					c.PostgreSQL.Majors[0]++
				case "agent":
					c.Agent.Tunnel.Maximum++
				case "line":
					c.ReleaseLine = "1.20.x"
				}
				changed, _ = json.Marshal(c)
			}
			if _, err := compareChartCompatibility(changed, m); err == nil {
				t.Fatal("unsigned chart compatibility difference admitted")
			}
		})
	}
}
func TestTargetChartPrivateReadRetainsInitialIdentityAndFiniteBytes(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "astronomer-1.2.2.tgz")
	if err := os.WriteFile(file, []byte("bound archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := bindMetadataArtifact(context.Background(), file, targetChartCompressedMaximum)
	if err != nil {
		t.Fatal(err)
	}
	c := &TargetChartContent{archivePath: file, input: input}
	if raw, err := c.readArchive(context.Background()); err != nil || string(raw) != "bound archive bytes" {
		t.Fatal("private read lost bytes", err)
	}
	if err := os.Rename(file, file+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("bound archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.readArchive(context.Background()); err == nil {
		t.Fatal("same-content recreated chart admitted")
	}
}

// Cancel at every observed context-read boundary of one successful real gzip/
// tar parse, including construction, metadata/body reads and footer draining.
// This deterministic owner context avoids time/scheduling assumptions and does
// not replace the archive or any reader/decoder with an asserted outcome.
func TestTargetChartParserPreservesCancellationWithoutPartialPublication(t *testing.T) {
	manifest, compatibility := chartCompatibilityFixture(t)
	entries := append(normalChartEntries(compatibility), chartArchiveEntry{name: "astronomer/values.yaml", data: []byte(runtimeValuesFixture)})
	raw := chartArchiveFixture(t, entries)
	control, stop := context.WithCancel(context.Background())
	defer stop()
	probe := &chartParserCancellationContext{Context: control, cancel: stop}
	var values []byte
	actual, err := targetChartCompatibilityWithValues(probe, raw, manifest, &values)
	if err != nil || !bytes.Equal(actual, compatibility) || string(values) != runtimeValuesFixture || probe.calls < 3 {
		t.Fatal("positive real archive/context-boundary control failed", err)
	}
	for cancelAt := 1; cancelAt <= probe.calls; cancelAt++ {
		t.Run(fmt.Sprintf("context-read-%03d", cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner := &chartParserCancellationContext{Context: ctx, cancel: cancel, cancelAt: cancelAt}
			values := []byte("prior-publication")
			actual, err := targetChartCompatibilityWithValues(owner, raw, manifest, &values)
			if !errors.Is(err, context.Canceled) || actual != nil || string(values) != "prior-publication" {
				t.Fatal("owned cancellation was translated or incomplete values were published", err)
			}
		})
	}
}

type chartParserCancellationContext struct {
	context.Context
	cancel          context.CancelFunc
	calls, cancelAt int
}

func (c *chartParserCancellationContext) Err() error {
	c.calls++
	if c.cancelAt > 0 && c.calls >= c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}
