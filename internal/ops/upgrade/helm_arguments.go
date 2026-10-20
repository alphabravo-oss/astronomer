package upgrade

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	digest "github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/registry"

	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// HelmInputs is assembled only after signature, exact artifact byte/checksum,
// chart/OCI equality, readiness/PDB and backup/schema gates. This helper merely
// preserves argv construction; parsing a Manifest is not cryptographic proof.
// RuntimeSources comes from the byte-verified target chart, not current defaults
// or an ambient latest lookup. No database major is selected here.
// HelmToolBinding is populated by the verified tool preflight, not PATH lookup.
// A declared hash is not proof until the adapter compares actual executable
// bytes and version output to the canonical selected tool authority.
type HelmToolBinding struct {
	Path    string
	Version string
	SHA256  string
}

func (tool HelmToolBinding) major() (uint64, error) {
	version, err := semver.StrictNewVersion(strings.TrimPrefix(tool.Version, "v"))
	hash, digestErr := digest.Parse(tool.SHA256)
	if err != nil || version.Prerelease() != "" || !filepath.IsAbs(tool.Path) || digestErr != nil || hash.Algorithm() != digest.SHA256 || hash.Validate() != nil {
		return 0, errors.New("verified exact Helm executable binding required")
	}
	if version.Major() != 3 && version.Major() != 4 {
		return 0, errors.New("Helm CLI semantics are not characterized")
	}
	return version.Major(), nil
}

type HelmInputs struct {
	Tool           HelmToolBinding
	Plan           Plan
	ChartReference string
	ManifestFile   string
	Manifest       releasecontract.Manifest
	RuntimeSources map[string]string
}

func (in HelmInputs) arguments() ([]string, error) {
	major, err := in.Tool.major()
	if err != nil {
		return nil, err
	}
	if in.Plan.Validate() != nil || !filepath.IsAbs(in.ManifestFile) || in.Manifest.Release.Version != in.Plan.Target {
		return nil, errors.New("exact target/manifest/plan binding required")
	}
	// A local archive is accepted only as an argv representation. Its content
	// and immutable filesystem binding belong to the higher concrete Driver.
	if filepath.IsAbs(in.ChartReference) {
		if filepath.Clean(in.ChartReference) != in.ChartReference || !strings.HasSuffix(in.ChartReference, ".tgz") {
			return nil, errors.New("exact absolute local chart archive required")
		}
	} else {
		chart, err := registry.ParseReference(strings.TrimPrefix(in.ChartReference, "oci://"))
		if err != nil || !strings.HasPrefix(in.ChartReference, "oci://") || chart.Reference != "" || chart.String() != strings.TrimPrefix(in.ChartReference, "oci://") {
			return nil, errors.New("exact OCI chart repository required")
		}
	}
	images, err := applicationBindings(in.Manifest, in.Plan.Target)
	if err != nil {
		return nil, err
	}
	runtime, err := RuntimeBindings(in.Manifest, in.RuntimeSources)
	if err != nil {
		return nil, err
	}
	args := []string{"--kube-context", in.Plan.Identity.Context, "upgrade", in.Plan.Identity.Release, in.ChartReference, "--namespace", in.Plan.Identity.Namespace, "--version", strings.TrimPrefix(in.Plan.Target, "v"), "--reset-then-reuse-values"}
	setting := func(key, value string) { args = append(args, "--set-string", key+"="+value) }
	setting("image.registry", "")
	for _, component := range []struct{ name, prefix, repository string }{
		{"server", "image.server", "astronomer-go-server"}, {"worker", "image.worker", "astronomer-go-worker"}, {"agent", "image.agent", "astronomer-go-agent"}, {"migrate", "image.migrate", "astronomer-go-migrate"}, {"frontend", "frontend.image", "astronomer-frontend"}, {"shell", "preflight.image", "astronomer-shell"},
	} {
		setting(component.prefix+".registry", "ghcr.io/alphabravo-oss")
		setting(component.prefix+".repository", component.repository)
		setting(component.prefix+".tag", in.Plan.Target)
		setting(component.prefix+".digest", referenceDigest(images[component.name]))
	}
	setting("config.agentImageRepository", images["agent"])
	setting("config.agentImageTag", in.Plan.Target)
	setting("kubectlShell.image", images["shell"])
	for _, role := range []string{"utilities.busybox", "postgres", "redis", "dex", "managementRestoreDrill.sidecar", "managementLogging"} {
		key := role + ".image.digest"
		if role == "utilities.busybox" {
			key = role + ".digest"
		}
		setting(key, referenceDigest(runtime[role]))
	}
	if dr := images["dr"]; dr != "" {
		setting("managementBackup.image.digest", referenceDigest(dr))
		setting("managementRestoreDrill.image.digest", referenceDigest(dr))
	}
	args = append(args, "--set-file", "release.manifest="+in.ManifestFile, "--timeout", in.Plan.Timeout.String())
	if major == 4 {
		args = append(args, "--server-side=false")
	}
	return args, nil
}
func (in HelmInputs) DryRunArguments() ([]string, error) {
	args, err := in.arguments()
	if err != nil {
		return nil, err
	}
	return append(args, "--dry-run=server", "--hide-secret"), nil
}
func (in HelmInputs) ApplyArguments() ([]string, error) {
	if in.Plan.Mode != Execute {
		return nil, errors.New("preview cannot construct apply command")
	}
	args, err := in.arguments()
	if err != nil {
		return nil, err
	}
	major, err := in.Tool.major()
	if err != nil {
		return nil, err
	}
	if major == 4 {
		return append(args, "--rollback-on-failure", "--cleanup-on-fail", "--wait=legacy", "--wait-for-jobs"), nil
	}
	return append(args, "--atomic", "--cleanup-on-fail", "--wait", "--wait-for-jobs"), nil
}
func applicationBindings(manifest releasecontract.Manifest, version string) (map[string]string, error) {
	expected := map[string]string{"server": "astronomer-go-server", "worker": "astronomer-go-worker", "agent": "astronomer-go-agent", "migrate": "astronomer-go-migrate", "frontend": "astronomer-frontend", "shell": "astronomer-shell"}
	parsed, err := semver.NewVersion(version)
	if err != nil {
		return nil, errors.New("exact release version required")
	}
	if !parsed.LessThan(semver.MustParse("1.2.0")) {
		expected["dr"] = "astronomer-dr"
	}
	if len(manifest.Astronomer.Images) != len(expected) {
		return nil, errors.New("exact historical six or current seven first-party images required")
	}
	result := make(map[string]string, len(expected))
	for _, image := range manifest.Astronomer.Images {
		repository, exists := expected[image.Name]
		expectedPrefix := "ghcr.io/alphabravo-oss/" + repository + "@"
		ref, parseErr := registry.ParseReference(image.Reference)
		hash, digestErr := digest.Parse(ref.Reference)
		if !exists || image.Kind != "container_image" || result[image.Name] != "" || parseErr != nil || digestErr != nil || ref.String() != image.Reference || hash.Algorithm() != digest.SHA256 || hash.Validate() != nil || image.Reference != expectedPrefix+hash.String() {
			return nil, errors.New("first-party image identity/digest differs")
		}
		result[image.Name] = image.Reference
	}
	return result, nil
}
func referenceDigest(reference string) string {
	return reference[strings.LastIndexByte(reference, '@')+1:]
}
