// Command release-assets produces the signed, closed release assets consumed
// by the server. Registry writes and keyless signing occur only with -publish.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"gopkg.in/yaml.v3"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type producer struct {
	out, version, owner string
	publish             bool
}
type subjects struct {
	Bundle, Flux                                          string
	Catalog                                               builtinbundles.Catalog
	CatalogDigest, BundleArchiveDigest, FluxArchiveDigest string
}

func main() {
	p := producer{}
	phase := flag.String("phase", "prepare", "prepare assets or generate manifest after chart publication")
	flag.StringVar(&p.out, "output", "dist/release", "output directory")
	flag.StringVar(&p.version, "version", "", "exact release tag")
	flag.StringVar(&p.owner, "owner", "ghcr.io/alphabravo-oss", "registry owner")
	flag.BoolVar(&p.publish, "publish", false, "publish exact assets and sign through GitHub OIDC")
	commit := flag.String("commit", "", "full source commit")
	epoch := flag.Int64("source-date-epoch", 0, "source commit timestamp")
	chart := flag.String("chart-reference", "", "published chart repository@digest")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	var err error
	switch *phase {
	case "prepare":
		err = p.prepare(ctx)
	case "manifest":
		err = p.manifest(ctx, *commit, *epoch, *chart)
	case "attest":
		err = p.sign(ctx, *chart)
	default:
		err = errors.New("unknown phase")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func hash(data []byte) string          { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }
func encode(value any) ([]byte, error) { return json.MarshalIndent(value, "", "  ") }
func (p producer) write(name string, data []byte) error {
	return os.WriteFile(filepath.Join(p.out, name), data, 0600)
}
func (p producer) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return data, nil
}
func (p producer) sign(ctx context.Context, ref string) error {
	if !p.publish {
		return nil
	}
	if _, err := p.command(ctx, "scripts/cosign-release.sh", "sign", "--yes", "--new-bundle-format=false", "--use-signing-config=false", ref); err != nil {
		return err
	}
	_, exact, _ := strings.Cut(ref, "@")
	sbom := map[string]any{"spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": "SPDXRef-DOCUMENT", "name": ref, "documentNamespace": "https://astronomer.io/spdx/" + strings.TrimPrefix(exact, "sha256:"), "creationInfo": map[string]any{"creators": []string{"Tool: astronomer-release-assets"}, "created": "1970-01-01T00:00:00Z"}, "packages": []any{map[string]any{"SPDXID": "SPDXRef-Artifact", "name": ref, "downloadLocation": "NOASSERTION", "filesAnalyzed": false, "checksums": []any{map[string]string{"algorithm": "SHA256", "checksumValue": strings.TrimPrefix(exact, "sha256:")}}}}}
	provenance := map[string]any{"builder": map[string]string{"id": "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml"}, "buildType": "https://astronomer.io/release-assets/v1", "invocation": map[string]any{"parameters": map[string]string{"version": p.version, "subject": ref}}}
	for _, predicate := range []struct {
		name, kind string
		value      any
	}{{"asset-sbom.spdx.json", "spdxjson", sbom}, {"asset-provenance.json", "slsaprovenance", provenance}} {
		data, err := encode(predicate.value)
		if err != nil {
			return err
		}
		if err := p.write(predicate.name, data); err != nil {
			return err
		}
		if _, err := p.command(ctx, "scripts/cosign-release.sh", "attest", "--yes", "--new-bundle-format=false", "--use-signing-config=false", "--predicate", filepath.Join(p.out, predicate.name), "--type", predicate.kind, ref); err != nil {
			return err
		}
	}
	return nil
}
func (p producer) artifact(ctx context.Context, role, media string, payload, config []byte, configMedia string) (string, error) {
	archive := role + ".tgz"
	configFile := role + "-config.json"
	if err := p.write(archive, payload); err != nil {
		return "", err
	}
	if err := p.write(configFile, config); err != nil {
		return "", err
	}
	layout := filepath.Join(p.out, "layout") + ":" + role
	_, err := p.command(ctx, "oras", "push", "--oci-layout", layout, "--annotation", "org.opencontainers.image.created=1970-01-01T00:00:00Z", "--config", filepath.Join(p.out, configFile)+":"+configMedia, filepath.Join(p.out, archive)+":"+media)
	if err != nil {
		return "", err
	}
	exact, err := p.command(ctx, "oras", "resolve", "--oci-layout", layout)
	if err != nil {
		return "", err
	}
	repo := p.owner + "/astronomer-" + role
	tag := p.version
	if strings.HasPrefix(role, "bundle-") {
		repo = p.owner + "/astronomer-platform-bundles"
		tag += "-" + role
	}
	ref := repo + "@" + strings.TrimSpace(string(exact))
	if p.publish {
		if _, err := p.command(ctx, "oras", "copy", "--from-oci-layout", layout, repo+":"+tag); err != nil {
			return "", err
		}
		if err := p.sign(ctx, ref); err != nil {
			return "", err
		}
	}
	return ref, nil
}
func archive(members map[string][]byte) ([]byte, error) {
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(members))
	for n := range members {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data := members[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Size: int64(len(data)), Mode: 0644, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func (p producer) prepare(ctx context.Context) error {
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$`).MatchString(p.version) {
		return errors.New("exact version required")
	}
	if err := os.MkdirAll(p.out, 0700); err != nil {
		return err
	}
	catalog, err := builtinbundles.Load()
	if err != nil {
		return err
	}
	catalog.Release = p.version

	databaseInputs, err := os.ReadFile("deploy/release/databases.json")
	if err != nil {
		return err
	}
	var databases map[string]string
	if err := json.Unmarshal(databaseInputs, &databases); err != nil {
		return err
	}
	members := map[string][]byte{}
	bom := distribution.BOM{SchemaVersion: 1, AssetContractVersion: distribution.DescriptorAssetContract, Release: p.version, Assets: []distribution.Asset{}}
	values := map[string]map[string]any{}
	inventory := distribution.Inventory{SchemaVersion: 1, Images: []distribution.Image{}}
	images := map[string]int{}
	for i := range catalog.Components {
		c := &catalog.Components[i]
		if _, err := p.command(ctx, "helm", "pull", c.Source.Chart, "--repo", c.Source.URL, "--version", c.Source.Version, "--destination", p.out); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(p.out, c.Source.Chart+"-"+c.Source.Version+".tgz"))
		if err != nil {
			return err
		}
		if hash(data) != c.Source.ChartDigest {
			return fmt.Errorf("%s: reviewed chart digest mismatch", c.Slug)
		}
		if err := distribution.ValidateChartArchive(data); err != nil {
			return err
		}
		config, err := json.Marshal(map[string]string{"name": c.Source.Chart, "version": c.Source.Version})
		if err != nil {
			return err
		}
		ref, err := p.artifact(ctx, "bundle-"+c.Slug, "application/vnd.cncf.helm.chart.content.v1.tar+gzip", data, config, "application/vnd.cncf.helm.config.v1+json")
		if err != nil {
			return err
		}
		repo, exact, _ := strings.Cut(ref, "@")
		bom.Charts = append(bom.Charts, distribution.ChartSubject{Component: c.Slug, Repository: p.owner, RepositoryName: strings.TrimPrefix(repo, p.owner+"/"), Digest: exact, ArchiveDigest: hash(data)})
		members["charts/"+c.Slug+".tgz"] = data
		if c.Source.Chart == "trivy-operator" {
			trivy, ok := c.Values["trivy"].(map[string]any)
			if !ok {
				return errors.New("missing scanner values")
			}
			for _, db := range []struct{ role, key, registry string }{{"trivy-database", "dbRepository", "dbRegistry"}, {"trivy-java-database", "javaDbRepository", "javaDbRegistry"}} {
				asset, err := p.database(ctx, db.role, databases[db.role], members)
				if err != nil {
					return err
				}
				bom.Assets = append(bom.Assets, asset)
				trivy[db.registry] = ""
				trivy[db.key] = asset.ArtifactSubject
			}
		}
		values[c.Slug] = c.Values
		for _, image := range c.Images {
			if index, ok := images[image]; ok {
				inventory.Images[index].Workloads = append(inventory.Images[index].Workloads, c.Slug)
				continue
			}
			raw, err := p.command(ctx, "oras", "manifest", "fetch", image)
			if err != nil {
				return err
			}
			var index ocispec.Index
			if err := json.Unmarshal(raw, &index); err != nil {
				return err
			}
			platforms := map[string]bool{}
			for _, m := range index.Manifests {
				if m.Platform != nil {
					platforms[m.Platform.OS+"/"+m.Platform.Architecture] = true
				}
			}
			if !platforms["linux/amd64"] || !platforms["linux/arm64"] {
				return fmt.Errorf("%s lacks release platforms", image)
			}
			images[image] = len(inventory.Images)
			inventory.Images = append(inventory.Images, distribution.Image{Reference: image, Platforms: []string{"linux/amd64", "linux/arm64"}, Workloads: []string{c.Slug}})
		}
	}
	catalogData, err := encode(catalog)
	if err != nil {
		return err
	}
	members["catalog.json"] = catalogData
	members["profiles/values.json"], err = encode(values)
	if err != nil {
		return err
	}
	members["profiles/images.json"], err = encode(inventory)
	if err != nil {
		return err
	}
	bom.CatalogDigest = hash(catalogData)
	bom.Profiles = []distribution.Profile{{Name: "builtins", Qualification: "qualified", ValuesPath: "profiles/values.json", InventoryPath: "profiles/images.json", RequiredCategories: []string{"catalog", "chart", "values", "image_inventory", "vulnerability_database"}, BlockedCategories: []string{}, Capabilities: []string{"vulnerability-scanning"}}}
	names := make([]string, 0, len(members))
	for n := range members {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		category := "vulnerability_database"
		switch {
		case n == "catalog.json":
			category = "catalog"
		case strings.HasPrefix(n, "charts/"):
			category = "chart"
		case n == "profiles/values.json":
			category = "values"
		case n == "profiles/images.json":
			category = "image_inventory"
		}
		bom.Members = append(bom.Members, distribution.Member{Path: n, Digest: hash(members[n]), Size: int64(len(members[n])), Category: category})
	}
	members["bom.json"], err = encode(bom)
	if err != nil {
		return err
	}
	bundle, err := archive(members)
	if err != nil {
		return err
	}
	if _, _, err := distribution.ReadArchive(bundle, p.version, bom.CatalogDigest, "builtins"); err != nil {
		return fmt.Errorf("produced bundle rejected: %w", err)
	}
	bundleRef, err := p.artifact(ctx, "platform-bundles", distribution.MediaType, bundle, []byte("{}"), ocispec.MediaTypeEmptyJSON)
	if err != nil {
		return err
	}
	fluxMembers := map[string][]byte{}
	for _, name := range []string{"VERSION", "LICENSE.flux2", "README.md", "checksums.txt", "install.yaml", "provenance.json", "trust-policy.json"} {
		fluxMembers["flux/"+name], err = os.ReadFile(filepath.Join("deploy/flux", name))
		if err != nil {
			return err
		}
	}
	flux, err := archive(fluxMembers)
	if err != nil {
		return err
	}
	fluxRef, err := p.artifact(ctx, "flux-distribution", "application/vnd.astronomer.flux-distribution.v1+tar+gzip", flux, []byte("{}"), ocispec.MediaTypeEmptyJSON)
	if err != nil {
		return err
	}
	data, err := encode(subjects{Bundle: bundleRef, Flux: fluxRef, Catalog: catalog, CatalogDigest: bom.CatalogDigest, BundleArchiveDigest: hash(bundle), FluxArchiveDigest: hash(flux)})
	if err != nil {
		return err
	}
	return p.write("subjects.json", data)
}
func (p producer) database(ctx context.Context, role, source string, members map[string][]byte) (distribution.Asset, error) {
	var empty distribution.Asset
	raw, err := p.command(ctx, "oras", "manifest", "fetch", source)
	if err != nil {
		return empty, err
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return empty, err
	}
	if len(manifest.Layers) != 1 {
		return empty, errors.New("database must have one layer")
	}
	repository, _, pinned := strings.Cut(source, "@")
	if !pinned {
		return empty, errors.New("database input must be pinned by digest")
	}
	var original string
	// command trims trailing whitespace; fetch exact bytes for digest binding.
	desc, err := p.command(ctx, "oras", "resolve", source)
	if err != nil {
		return empty, err
	}
	original = repository + "@" + strings.TrimSpace(string(desc))
	raw, err = p.command(ctx, "oras", "manifest", "fetch", original)
	if err != nil {
		return empty, err
	}
	if hash(raw) != strings.TrimSpace(string(desc)) {
		return empty, errors.New("database manifest bytes changed")
	}
	config, err := p.command(ctx, "oras", "blob", "fetch", "--output", "-", repository+"@"+manifest.Config.Digest.String())
	if err != nil {
		return empty, err
	}
	if hash(config) != manifest.Config.Digest.String() {
		return empty, errors.New("database config digest mismatch")
	}
	mp, cp := "databases/"+role+"-manifest.json", "databases/"+role+"-config.json"
	members[mp], members[cp] = raw, config
	dest := p.owner + "/astronomer-platform-bundles"
	published := dest + "@" + strings.TrimSpace(string(desc))
	if p.publish {
		if _, err := p.command(ctx, "oras", "copy", original, dest+":"+p.version+"-"+role); err != nil {
			return empty, err
		}
		if err := p.sign(ctx, published); err != nil {
			return empty, err
		}
	}
	layer := manifest.Layers[0]
	return distribution.Asset{ID: role, MemberPath: mp, Images: []string{}, ArtifactSubject: original, PublishedSubject: published, ArtifactManifestPath: mp, ArtifactConfigPath: cp, DatabaseDescriptor: &distribution.DatabaseDescriptor{Digest: layer.Digest.String(), Size: layer.Size, MediaType: layer.MediaType}}, nil
}

func artifact(name, kind, ref, content string) releasecontract.Artifact {
	if content == "" {
		_, content, _ = strings.Cut(ref, "@")
	}
	return releasecontract.Artifact{Name: name, Kind: kind, Reference: ref, ContentDigest: content, Evidence: releasecontract.Evidence{Signature: "cosign://" + ref, SBOM: "cosign-attestation://" + ref + "#spdxjson", Provenance: "cosign-attestation://" + ref + "#slsaprovenance"}}
}
func (p producer) manifest(ctx context.Context, commit string, epoch int64, chart string) error {
	if len(commit) != 40 || epoch <= 0 {
		return errors.New("full commit and source timestamp required")
	}
	data, err := os.ReadFile("deploy/release/external-artifacts.json")
	if err != nil {
		return err
	}
	var m releasecontract.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	data, err = os.ReadFile(filepath.Join(p.out, "subjects.json"))
	if err != nil {
		return err
	}
	var assets subjects
	if err := json.Unmarshal(data, &assets); err != nil {
		return err
	}
	if assets.Catalog.Release != p.version {
		return errors.New("prepared assets bind another release")
	}
	m.SchemaVersion = 1
	m.Release.Version = p.version
	m.Release.SourceCommit = commit
	m.Release.GeneratedAt = time.Unix(epoch, 0).UTC().Format(time.RFC3339)
	m.Release.InstallMode = "fresh_and_upgrade"
	m.Release.SigningPolicy.CertificateOIDCIssuer = "https://token.actions.githubusercontent.com"
	m.Release.SigningPolicy.CertificateIdentity = "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + p.version
	m.Compatibility.Kubernetes.MinimumMinor = "1.33"
	m.Compatibility.Kubernetes.MaximumMinor = "1.35"
	m.Compatibility.AgentProtocol.Name = "astronomer.delivery"
	m.Compatibility.AgentProtocol.Minimum = protocol.MinimumDeliveryProtocolVersion
	m.Compatibility.AgentProtocol.Maximum = protocol.MaximumDeliveryProtocolVersion
	m.Compatibility.AgentProtocol.RequiredCapabilities = protocol.RequiredConnectCapabilities()
	m.Compatibility.Agent.MinimumCompatibleVersion = protocol.MinimumCompatibleAgentVersion
	m.Compatibility.Agent.MinimumSupportedVersion = protocol.MinimumSupportedAgentVersion
	m.Compatibility.Agent.MaximumSupportedVersionExclusive = protocol.MaximumSupportedAgentVersionExclusive
	m.Compatibility.Agent.TunnelProtocolMinimum = protocol.MinimumTunnelProtocolVersion
	m.Compatibility.Agent.TunnelProtocolMaximum = protocol.MaximumTunnelProtocolVersion
	m.Compatibility.Agent.HeartbeatSchemaMinimum = protocol.MinimumHeartbeatSchemaVersion
	m.Compatibility.Agent.HeartbeatSchemaMaximum = protocol.MaximumHeartbeatSchemaVersion
	// Read the chart's schema contract so a new migration cannot silently ship
	// an old manifest schema target.
	data, err = os.ReadFile("deploy/chart/files/release-compatibility.json")
	if err != nil {
		return err
	}
	var compatibility struct {
		PostgreSQL struct {
			SupportedMajors []int `json:"supported_majors"`
			Minimum         int   `json:"minimum_upgrade_schema"`
			Target          int   `json:"target_schema"`
			Reversible      int   `json:"reversible_through_schema"`
		} `json:"postgresql"`
	}
	if err := json.Unmarshal(data, &compatibility); err != nil {
		return err
	}
	pg := compatibility.PostgreSQL
	m.Compatibility.PostgreSQL.SupportedMajors = pg.SupportedMajors
	m.Compatibility.PostgreSQL.MinimumUpgradeSchema = pg.Minimum
	m.Compatibility.PostgreSQL.TargetSchema = pg.Target
	m.Compatibility.PostgreSQL.ReversibleThroughSchema = pg.Reversible
	firstParty := map[string]string{}
	for _, role := range []string{"agent", "dr", "frontend", "server", "shell", "worker"} {
		raw, err := os.ReadFile(filepath.Join(p.out, role+".digest"))
		if err != nil {
			return err
		}
		ref := strings.TrimSpace(string(raw))
		repo, _, _ := strings.Cut(ref, "@")
		firstParty[repo] = ref
		m.Astronomer.Images = append(m.Astronomer.Images, artifact(role, "container_image", ref, ""))
	}
	m.Astronomer.Chart = artifact("astronomer-chart", "helm_chart", chart, "")
	data, err = os.ReadFile("deploy/chart/images.txt")
	if err != nil {
		return err
	}
	for _, source := range strings.Split(string(data), "\n") {
		source = strings.TrimSpace(source)
		if source == "" || strings.HasPrefix(source, "#") {
			continue
		}
		var ref string
		repo, _, pinned := strings.Cut(source, "@")
		if colon := strings.LastIndex(repo, ":"); colon > strings.LastIndex(repo, "/") {
			repo = repo[:colon]
		}
		if replacement, ok := firstParty[repo]; ok {
			ref = replacement
		} else if !pinned {
			canonical := source
			if !strings.Contains(source, "/") || !strings.ContainsAny(strings.Split(source, "/")[0], ".:") {
				if !strings.Contains(source, "/") {
					canonical = "docker.io/library/" + source
				} else {
					canonical = "docker.io/" + source
				}
			}
			resolved, err := p.command(ctx, "oras", "resolve", canonical)
			if err != nil {
				return err
			}
			canonicalRepo, _, _ := strings.Cut(canonical, "@")
			if colon := strings.LastIndex(canonicalRepo, ":"); colon > strings.LastIndex(canonicalRepo, "/") {
				canonicalRepo = canonicalRepo[:colon]
			}
			ref = canonicalRepo + "@" + strings.TrimSpace(string(resolved))
		} else {
			ref = canonicalRepository(repo) + "@" + strings.SplitN(source, "@", 2)[1]
		}
		m.Astronomer.RuntimeImages = append(m.Astronomer.RuntimeImages, releasecontract.RuntimeImage{SourceReference: source, Reference: ref})
	}
	m.Flux.Version = fluxdistribution.Version()
	m.Flux.Distribution = artifact("astronomer-flux-distribution", "oci_artifact", assets.Flux, assets.FluxArchiveDigest)
	controllers, err := fluxdistribution.ControllerImages()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(controllers))
	for n := range controllers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := controllers[n]
		m.Flux.Controllers = append(m.Flux.Controllers, artifact(n, "container_image", c.Reference[:strings.LastIndex(c.Reference, ":")]+"@"+c.Digest, ""))
	}
	m.Flux.APIs = []string{"source.toolkit.fluxcd.io/v1", "kustomize.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"}
	m.BuiltInBundles.Artifact = artifact("astronomer-platform-bundles", "oci_artifact", assets.Bundle, assets.BundleArchiveDigest)
	m.BuiltInBundles.CatalogDigest = assets.CatalogDigest
	for _, c := range assets.Catalog.Components {
		m.BuiltInBundles.Components = append(m.BuiltInBundles.Components, struct {
			Slug         string   `json:"slug"`
			Chart        string   `json:"chart"`
			ChartVersion string   `json:"chart_version"`
			ChartDigest  string   `json:"chart_digest"`
			Images       []string `json:"images"`
		}{c.Slug, c.Source.Chart, c.Source.Version, c.Source.ChartDigest, c.Images})
	}
	if _, err := m.Validate(p.version); err != nil {
		return err
	}
	data, err = encode(m)
	if err != nil {
		return err
	}
	if err := p.write("release-manifest.json", data); err != nil {
		return err
	}
	values, err := yaml.Marshal(map[string]any{"release": map[string]string{"manifest": string(data)}})
	if err != nil {
		return err
	}
	return p.write("release-values.yaml", values)
}

func canonicalRepository(repository string) string {
	if !strings.Contains(repository, "/") {
		return "docker.io/library/" + repository
	}
	if !strings.ContainsAny(strings.Split(repository, "/")[0], ".:") {
		return "docker.io/" + repository
	}
	return repository
}
