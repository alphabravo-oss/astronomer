//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	yaml "gopkg.in/yaml.v3"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	sigsyaml "sigs.k8s.io/yaml"
)

const targetChartCompressedMaximum = int64(64 << 20)
const targetChartExpandedMaximum = int64(128 << 20)
const targetChartEntriesMaximum = 4096
const helmChartContentType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// TargetChartContent establishes signed pinned OCI payload equality with the
// initially captured release archive and reads compatibility from those exact
// bytes. It does not grant GitHub attestation, first-party image verification,
// database replacement or a complete Driver Verify result.
// The caller must serialize this handle's methods with its Driver invocation.
type TargetChartContent struct {
	metadata    *SignedReleaseMetadata
	client      *http.Client
	input       metadataInput
	archivePath string
}

type ChartSchemaBounds struct {
	Minimum, Target, Reversible uint32
	SupportedMajors             []uint32
}

type chartRange struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

type chartCompatibility struct {
	SchemaVersion int    `json:"schema_version"`
	ReleaseLine   string `json:"release_line"`
	Agent         struct {
		FutureMajorPolicy                string     `json:"future_major_policy"`
		Heartbeat                        chartRange `json:"heartbeat_schema"`
		MaximumSupportedVersionExclusive string     `json:"maximum_supported_version_exclusive"`
		MinimumCompatibleVersion         string     `json:"minimum_compatible_version"`
		MinimumSupportedVersion          string     `json:"minimum_supported_version"`
		SupportWindow                    int        `json:"support_window_release_lines"`
		Tunnel                           chartRange `json:"tunnel_protocol"`
	} `json:"agent"`
	Protocol struct {
		Name          string       `json:"name"`
		Capabilities  []string     `json:"required_capabilities"`
		Ranges        []chartRange `json:"supported_ranges"`
		UnknownPolicy string       `json:"unknown_field_policy"`
	} `json:"agent_protocol"`
	PostgreSQL struct {
		MigrationMode string   `json:"migration_mode"`
		Minimum       int64    `json:"minimum_upgrade_schema"`
		Reversible    int64    `json:"reversible_through_schema"`
		Majors        []uint32 `json:"supported_majors"`
		UpgradeLines  []string `json:"supported_upgrade_release_lines"`
		Target        int64    `json:"target_schema"`
		TLSRequired   bool     `json:"tls_required_in_production"`
	} `json:"postgresql"`
}

// Network authority comes from independently injected operator destination
// options. Neither chart/manifest URLs nor public artifact data can grant CIDRs,
// proxies, credential forwarding or redirect origins. All actual ORAS and
// signature requests use the canonical guarded client and owner context.
func NewTargetChartContent(ctx context.Context, metadata *SignedReleaseMetadata, policy httpclient.DestinationPolicy, options httpclient.DestinationOptions) (*TargetChartContent, error) {
	if ctx == nil || metadata == nil {
		return nil, errors.New("explicit signed metadata and chart context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	archivePath := filepath.Join(metadata.directory, "astronomer-"+strings.TrimPrefix(metadata.target, "v")+".tgz")
	input, err := bindMetadataArtifact(ctx, archivePath, targetChartCompressedMaximum)
	if err != nil {
		return nil, err
	}
	// Independent finite policy applies to every canonical response, including
	// signature payloads and registry manifests. Earlier owner deadlines win.
	options.MaxResponseBytes = targetChartCompressedMaximum
	client, err := httpclient.NewDestinationClientContext(ctx, policy, options)
	if err != nil {
		return nil, err
	}
	c := &TargetChartContent{metadata: metadata, client: client, input: input, archivePath: archivePath}
	if _, _, err := c.Verify(ctx); err != nil {
		client.CloseIdleConnections()
		return nil, err
	}
	return c, nil
}

func (c *TargetChartContent) Close() {
	if c != nil && c.client != nil {
		c.client.CloseIdleConnections()
	}
}

// Verify returns a fresh private compatibility projection after the actual
// pinned signature, exact OCI layer digest, local archive digest and strict
// finite archive parser all pass. Every later consumer must repeat this method
// before using schema bounds; the exported projection itself is not authority.
func (c *TargetChartContent) Verify(ctx context.Context) (ChartSchemaBounds, []byte, error) {
	var empty ChartSchemaBounds
	if ctx == nil || c == nil || c.metadata == nil || c.client == nil {
		return empty, nil, errors.New("explicit target chart ownership required")
	}
	manifest, err := c.metadata.Verify(ctx)
	if err != nil {
		return empty, nil, err
	}
	item := manifest.Astronomer.Chart
	if item.Kind != "helm_chart" {
		return empty, nil, errors.New("signed target is not a Helm chart")
	}
	raw, err := c.readArchive(ctx)
	if err != nil {
		return empty, nil, err
	}
	if fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) != item.ContentDigest {
		return empty, nil, errors.New("release chart bytes differ from signed content digest")
	}
	split := strings.Split(item.Reference, "@")
	if len(split) != 2 {
		return empty, nil, errors.New("immutable signed chart subject required")
	}
	// Legacy upgrade verifies OCI signatures against the normal release publisher
	// even when interrupted-release metadata uses the separate resume publisher.
	trust := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Issuer: metadataIssuer, Identity: "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + c.metadata.target}
	payload, _, err := resolver.FetchVerifiedOCIArtifact(ctx, c.client, split[0], split[1], helmChartContentType, trust, c.metadata.trust.FrozenDirectory(), targetChartCompressedMaximum)
	if err != nil {
		return empty, nil, err
	}
	if !bytes.Equal(raw, payload) {
		return empty, nil, errors.New("signed pinned OCI chart is not the exact release archive")
	}
	compatibility, err := targetChartCompatibility(ctx, raw, manifest)
	if err != nil {
		return empty, nil, err
	}
	bounds, err := compareChartCompatibility(compatibility, manifest)
	if err != nil {
		return empty, nil, err
	}
	if _, err := c.readArchive(ctx); err != nil {
		return empty, nil, err
	}
	if _, err := c.metadata.Verify(ctx); err != nil {
		return empty, nil, err
	}
	return bounds, append([]byte(nil), compatibility...), ctx.Err()
}

func (c *TargetChartContent) readArchive(ctx context.Context) ([]byte, error) {
	bound, err := bindMetadataArtifact(ctx, c.archivePath, targetChartCompressedMaximum)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(bound.identity, c.input.identity) || bound.hash != c.input.hash {
		return nil, errors.New("initial target chart identity or bytes changed")
	}
	source, err := os.OpenFile(c.archivePath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("open bound target chart")
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !sameArtifact(c.input.identity, opened) {
		return nil, errors.New("bound target chart changed before read")
	}
	raw, err := io.ReadAll(io.LimitReader(chartContextReader{ctx, source}, targetChartCompressedMaximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > targetChartCompressedMaximum || len(raw) == 0 || sha256.Sum256(raw) != c.input.hash {
		return nil, errors.New("target chart payload exceeded or changed binding")
	}
	after, err := source.Stat()
	if err != nil || !sameArtifact(opened, after) {
		return nil, errors.New("target chart identity changed during read")
	}
	current, err := os.Lstat(c.archivePath)
	if err != nil || !sameArtifact(opened, current) {
		return nil, errors.New("target chart name changed during read")
	}
	return raw, ctx.Err()
}

type chartByteReader struct {
	chartContextReader
	bytes io.ByteReader
}

func (r chartByteReader) ReadByte() (byte, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.bytes.ReadByte()
}

type chartContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r chartContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// No extraction occurs. Single gzip member, finite decompressed bytes/entries,
// duplicate paths, traversal, links and nested dependencies are rejected before
// any archive entry can become a filesystem or schema authority.
func targetChartCompatibility(ctx context.Context, raw []byte, manifest releasecontract.Manifest) ([]byte, error) {
	return targetChartCompatibilityWithValues(ctx, raw, manifest, nil)
}

// Values are returned only after the entire original finite archive validation.
func targetChartCompatibilityWithValues(ctx context.Context, raw []byte, manifest releasecontract.Manifest, values *[]byte) (result []byte, parseErr error) {
	if ctx == nil || len(raw) == 0 || int64(len(raw)) > targetChartCompressedMaximum {
		return nil, errors.New("explicit bounded chart parser input and context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// gzip/tar readers can return the owned context error before their format
	// checks. Preserve it across every sanitized gzip/header/body/footer error
	// translation; incomplete parses never publish compatibility or values.
	defer func() {
		if parseErr != nil {
			if err := ctx.Err(); err != nil {
				result, parseErr = nil, err
			}
		}
	}()
	compressed := bytes.NewReader(raw)
	gzipReader, err := gzip.NewReader(chartByteReader{chartContextReader{ctx, compressed}, compressed})
	if err != nil {
		return nil, errors.New("target chart gzip is invalid")
	}
	defer gzipReader.Close()
	gzipReader.Multistream(false)
	expanded := &io.LimitedReader{R: chartContextReader{ctx, gzipReader}, N: targetChartExpandedMaximum + 1}
	archive := tar.NewReader(expanded)
	seen := make(map[string]bool)
	var metadata, compatibility, chartValues []byte
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= targetChartEntriesMaximum {
			return nil, errors.New("target chart archive is invalid or exceeds entry budget")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if path.Clean(name) != name || !strings.HasPrefix(name, "astronomer/") && name != "astronomer" || strings.ContainsAny(name, "\\\x00\r\n") || seen[name] || (name == "astronomer/charts" || strings.HasPrefix(name, "astronomer/charts/")) || header.Size < 0 || header.Size > targetChartExpandedMaximum || header.Linkname != "" {
			return nil, errors.New("target chart entry identity is invalid")
		}
		seen[name] = true
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse") {
				return nil, errors.New("sparse target chart content is unsupported")
			}
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return nil, errors.New("chart directory contains bytes")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, errors.New("target chart entry is not regular content")
		}
		if name == "astronomer/Chart.yaml" || name == "astronomer/files/release-compatibility.json" || values != nil && name == "astronomer/values.yaml" {
			if header.Size <= 0 || header.Size > 1<<20 {
				return nil, errors.New("target chart metadata exceeds parser budget")
			}
			data, err := io.ReadAll(io.LimitReader(archive, (1<<20)+1))
			if err != nil || int64(len(data)) != header.Size {
				return nil, errors.New("target chart metadata is truncated")
			}
			if name == "astronomer/Chart.yaml" {
				metadata = data
			} else if name == "astronomer/values.yaml" {
				chartValues = data
			} else {
				compatibility = data
			}
		} else if _, err := io.CopyBuffer(io.Discard, chartContextReader{ctx, archive}, make([]byte, 32<<10)); err != nil {
			return nil, err
		}
		if expanded.N <= 0 {
			return nil, errors.New("target chart decompression budget exceeded")
		}
	}
	// Drain remaining tar padding to check gzip footer/checksum, with a small
	// independent zero-padding budget instead of hiding another archive/member.
	padding, err := io.ReadAll(io.LimitReader(expanded, (512<<10)+1))
	if err != nil || len(padding) > 512<<10 || expanded.N <= 0 {
		return nil, errors.New("target chart footer or padding exceeds bounds")
	}
	for _, b := range padding {
		if b != 0 {
			return nil, errors.New("target chart has trailing content")
		}
	}
	if compressed.Len() != 0 {
		return nil, errors.New("target chart has concatenated or trailing compressed content")
	}
	if len(metadata) == 0 || len(compatibility) == 0 {
		return nil, errors.New("target chart required metadata missing")
	}
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(metadata))
	if decoder.Decode(&node) != nil || decoder.Decode(&node) != io.EOF {
		return nil, errors.New("target chart metadata is not one YAML document")
	}
	normalized, err := sigsyaml.YAMLToJSONStrict(metadata)
	if err != nil {
		return nil, errors.New("target chart metadata YAML is ambiguous")
	}
	var chartMetadata chart.Metadata
	if jsoncontract.Decode(normalized, &chartMetadata) != nil || chartMetadata.Name != "astronomer" || chartMetadata.APIVersion != "v2" || chartMetadata.Version != strings.TrimPrefix(manifest.Release.Version, "v") || strings.TrimPrefix(chartMetadata.AppVersion, "v") != strings.TrimPrefix(manifest.Release.Version, "v") || len(chartMetadata.Dependencies) != 0 || chartMetadata.Type != "" && chartMetadata.Type != "application" {
		return nil, errors.New("target chart metadata differs from signed stable application")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if values != nil {
		if len(chartValues) == 0 {
			return nil, errors.New("target chart runtime values missing")
		}
		*values = append([]byte(nil), chartValues...)
	}
	return compatibility, nil
}

func compareChartCompatibility(raw []byte, manifest releasecontract.Manifest) (ChartSchemaBounds, error) {
	var empty ChartSchemaBounds
	var c chartCompatibility
	if len(raw) == 0 || len(raw) > 1<<20 || jsoncontract.Decode(raw, &c) != nil {
		return empty, errors.New("target chart compatibility JSON contract differs")
	}
	m := manifest.Compatibility
	if c.SchemaVersion != 1 || c.Protocol.Name != m.AgentProtocol.Name || c.Protocol.UnknownPolicy != "reject" || len(c.Protocol.Ranges) != 1 || c.Protocol.Ranges[0].Minimum != m.AgentProtocol.Minimum || c.Protocol.Ranges[0].Maximum != m.AgentProtocol.Maximum || !sameChartStringSet(c.Protocol.Capabilities, m.AgentProtocol.RequiredCapabilities) || c.Agent.FutureMajorPolicy != "reject" || c.Agent.SupportWindow <= 0 || c.Agent.MinimumCompatibleVersion != m.Agent.MinimumCompatibleVersion || c.Agent.MinimumSupportedVersion != m.Agent.MinimumSupportedVersion || c.Agent.MaximumSupportedVersionExclusive != m.Agent.MaximumSupportedVersionExclusive || c.Agent.Tunnel.Minimum != m.Agent.TunnelProtocolMinimum || c.Agent.Tunnel.Maximum != m.Agent.TunnelProtocolMaximum || c.Agent.Heartbeat.Minimum != m.Agent.HeartbeatSchemaMinimum || c.Agent.Heartbeat.Maximum != m.Agent.HeartbeatSchemaMaximum {
		return empty, errors.New("target chart agent compatibility differs from signed release")
	}
	p := c.PostgreSQL
	if p.MigrationMode != "versioned_incremental" || !p.TLSRequired || p.Minimum < 1 || p.Target < p.Minimum || p.Target > 1<<32-1 || p.Reversible < 0 || p.Reversible > p.Target || p.Minimum != int64(m.PostgreSQL.MinimumUpgradeSchema) || p.Target != int64(m.PostgreSQL.TargetSchema) || p.Reversible != int64(m.PostgreSQL.ReversibleThroughSchema) || len(p.Majors) != len(m.PostgreSQL.SupportedMajors) || len(p.Majors) == 0 || len(p.UpgradeLines) == 0 {
		return empty, errors.New("target chart PostgreSQL compatibility differs from signed release")
	}
	seen := map[uint32]bool{}
	for _, major := range p.Majors {
		if major == 0 || seen[major] {
			return empty, errors.New("target chart PostgreSQL major set differs")
		}
		seen[major] = true
	}
	for _, major := range m.PostgreSQL.SupportedMajors {
		if major <= 0 || int64(major) > 1<<32-1 || !seen[uint32(major)] {
			return empty, errors.New("target chart PostgreSQL major set differs")
		}
	}
	version := strings.Split(strings.TrimPrefix(manifest.Release.Version, "v"), ".")
	if len(version) != 3 || c.ReleaseLine != version[0]+"."+version[1]+".x" {
		return empty, errors.New("target chart release line differs")
	}
	return ChartSchemaBounds{Minimum: uint32(p.Minimum), Target: uint32(p.Target), Reversible: uint32(p.Reversible), SupportedMajors: append([]uint32(nil), p.Majors...)}, nil
}

func sameChartStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for i, v := range a {
		if v == "" || v != b[i] || i > 0 && a[i-1] == v {
			return false
		}
	}
	return true
}
