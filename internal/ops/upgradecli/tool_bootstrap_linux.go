//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"

	canonical "github.com/alphabravocompany/astronomer-go/internal/ops/upgradecli/toolchain"
)

// BootstrapToolkit is derived solely from an authenticated canonical resolution
// and exact primary artifacts in the operator's offline kit. The archive/root
// originals and partially written outputs remain retained on any failure.
// This admits tool bytes; actual Commands.VerifyTool still checks joined output.
type BootstrapToolkit struct {
	Helm, Kubectl, GH Tool
	TrustDirectory    string
}

type bootstrapInput struct {
	path    string
	maximum int64
	bound   metadataInput
}

func readBootstrapInput(ctx context.Context, directory, name string, maximum int64, expected string) ([]byte, bootstrapInput, error) {
	var empty bootstrapInput
	if !filepath.IsAbs(directory) || filepath.Base(name) != name {
		return nil, empty, errors.New("exact private bootstrap artifact scope required")
	}
	file := filepath.Join(directory, name)
	bound, err := bindMetadataArtifact(ctx, file, maximum)
	if err != nil {
		return nil, empty, err
	}
	digest := "sha256:" + hex.EncodeToString(bound.hash[:])
	if expected != "" && expected != digest {
		return nil, empty, errors.New("primary bootstrap artifact digest differs")
	}
	raw, err := readCapture(directory, name, maximum, digest)
	if err != nil {
		return nil, empty, err
	}
	after, err := bindMetadataArtifact(ctx, file, maximum)
	if err != nil || !sameArtifact(bound.identity, after.identity) || bound.hash != after.hash {
		return nil, empty, errors.New("bootstrap input changed while reading")
	}
	return raw, bootstrapInput{file, maximum, bound}, ctx.Err()
}

func recheckBootstrapInputs(ctx context.Context, inputs []bootstrapInput) error {
	for _, initial := range inputs {
		actual, err := bindMetadataArtifact(ctx, initial.path, initial.maximum)
		if err != nil || !sameArtifact(initial.bound.identity, actual.identity) || initial.bound.hash != actual.hash {
			return errors.New("initial bootstrap kit input changed")
		}
	}
	return ctx.Err()
}

func privateBootstrapDirectory(directory string) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("absolute private bootstrap directory required")
	}
	current, err := os.Lstat(directory)
	if err != nil || !current.IsDir() || current.Mode().Perm() != 0700 || current.Sys().(*syscall.Stat_t).Uid != safeconv.Uint32(os.Getuid()) {
		return errors.New("owned private bootstrap directory required")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return errors.New("private bootstrap directory alias forbidden")
	}
	return nil
}

// writeBootstrapBytes is exclusive and preserves any partial output. Inputs
// and trusted roots are never removed. A failed write returns no usable Tool.
func writeBootstrapBytes(ctx context.Context, directory, name string, raw []byte, mode os.FileMode) error {
	if ctx == nil {
		return errors.New("explicit bootstrap publication context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := privateBootstrapDirectory(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errors.New("open private bootstrap directory")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return errors.New("exclusive bootstrap output unavailable")
	}
	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
		}
	}()
	for len(raw) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		count := len(raw)
		if count > 32<<10 {
			count = 32 << 10
		}
		n, err := file.Write(raw[:count])
		if err != nil || n != count {
			return errors.New("write bootstrap output")
		}
		raw = raw[count:]
	}
	if err := file.Sync(); err != nil {
		return errors.New("sync bootstrap output")
	}
	if err := file.Close(); err != nil {
		return errors.New("close bootstrap output")
	}
	complete = true
	parent, err := os.Open(directory)
	if err != nil {
		return errors.New("open bootstrap durability scope")
	}
	defer func(close func() error) { _ = close() }(parent.Close)
	if err := parent.Sync(); err != nil {
		return errors.New("sync bootstrap directory")
	}
	return ctx.Err()
}

// BootstrapTools is offline and makes no process, PATH, network, proxy or
// credential lookup. The executable supplies its compile-time source commit,
// independent target argv and two owned private directories. Its compiled
// authority/trusted root are the sole selection and trust sources.
func BootstrapTools(ctx context.Context, kitDirectory, outputDirectory, sourceCommit, target string) (BootstrapToolkit, error) {
	var empty BootstrapToolkit
	if ctx == nil || runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || kitDirectory == outputDirectory {
		return empty, errors.New("supported exact offline bootstrap scopes required")
	}
	if err := privateBootstrapDirectory(kitDirectory); err != nil {
		return empty, err
	}
	if err := privateBootstrapDirectory(outputDirectory); err != nil {
		return empty, err
	}
	record, recordInput, err := readBootstrapInput(ctx, kitDirectory, "toolchain-resolution.json", 1<<20, "")
	if err != nil {
		return empty, err
	}
	bundle, bundleInput, err := readBootstrapInput(ctx, kitDirectory, "toolchain-resolution.sigstore.json", 1<<20, "")
	if err != nil {
		return empty, err
	}
	// Materialize the immutable compiled root first. Failure never accepts a root
	// already present in outputDirectory or substitutes an operator trust file.
	if err := artifactCapacity(outputDirectory, int64(len(canonical.TrustedRootBytes()))); err != nil {
		return empty, err
	}
	if err := writeBootstrapBytes(ctx, outputDirectory, "trusted_root.json", canonical.TrustedRootBytes(), 0600); err != nil {
		return empty, err
	}
	authority, err := verifyBootstrapResolution(ctx, record, bundle, outputDirectory, sourceCommit, target)
	if err != nil {
		return empty, err
	}
	inputs := []bootstrapInput{recordInput, bundleInput}
	kinds := []ToolKind{Helm, Kubectl, GitHubCLI}
	componentNames := []string{"helm", "kubectl-shell", "github-cli"}
	bytesToInstall := make([][]byte, 3)
	tuples := make([]ExecutableArtifact, 3)
	total := int64(0)
	for i, name := range componentNames {
		tuple, err := selectBootstrapArtifact(authority, name, "linux/"+runtime.GOARCH)
		if err != nil {
			return empty, err
		}
		tuples[i] = tuple
		raw, input, err := readBootstrapInput(ctx, kitDirectory, tuple.Name, tuple.SizeBytes, tuple.Digest)
		if err != nil {
			return empty, err
		}
		inputs = append(inputs, input)
		executable, err := decodeToolArtifact(ctx, tuple, raw)
		if err != nil {
			return empty, err
		}
		bytesToInstall[i] = executable
		total += int64(len(executable))
	}
	if err := recheckBootstrapInputs(ctx, inputs); err != nil {
		return empty, err
	}
	if err := artifactCapacity(outputDirectory, total); err != nil {
		return empty, err
	}
	tools := make([]Tool, 3)
	for i, name := range []string{"helm", "kubectl", "gh"} {
		if err := writeBootstrapBytes(ctx, outputDirectory, name, bytesToInstall[i], 0700); err != nil {
			return empty, err
		}
		hash := sha256.Sum256(bytesToInstall[i])
		version := "v" + tuples[i].Version
		tools[i] = Tool{Kind: kinds[i], Path: filepath.Join(outputDirectory, name), Version: version, SHA256: "sha256:" + hex.EncodeToString(hash[:])}
		if _, err := toolIdentity(tools[i]); err != nil {
			return empty, err
		}
	}
	if err := recheckBootstrapInputs(ctx, inputs); err != nil {
		return empty, err
	}
	return BootstrapToolkit{tools[0], tools[1], tools[2], outputDirectory}, ctx.Err()
}

func selectBootstrapArtifact(authority bootstrapAuthority, name, platform string) (ExecutableArtifact, error) {
	var empty ExecutableArtifact
	for _, component := range authority.Components {
		if component.ID != name {
			continue
		}
		if component.Selected == nil || !bootstrapSelectionAllowed(component, time.Now()) {
			return empty, errors.New("bootstrap executable requires canonical stable selection or current owned kubectl skew exception")
		}
		if len(component.Platforms) != 2 || ((component.Platforms[0] != "linux/amd64" || component.Platforms[1] != "linux/arm64") && (component.Platforms[1] != "linux/amd64" || component.Platforms[0] != "linux/arm64")) {
			return empty, errors.New("canonical executable declared platforms differ")
		}
		var selected *ExecutableArtifact
		seen := make(map[string]bool)
		for i := range component.Executables {
			artifact := component.Executables[i]
			if artifact.Version != *component.Selected || artifact.SizeBytes <= 0 || artifact.SizeBytes > 512<<20 || seen[artifact.Platform] || !validBootstrapTuple(name, artifact) {
				return empty, errors.New("canonical executable tuple contradiction")
			}
			seen[artifact.Platform] = true
			sourceBound := false
			for _, proof := range component.SourceBindings {
				if proof.Path == artifact.Provenance && contentSHA.MatchString(proof.Digest) {
					sourceBound = true
				}
			}
			if !sourceBound {
				return empty, errors.New("canonical executable tuple proof missing")
			}
			if artifact.Platform == platform {
				copy := artifact
				selected = &copy
			}
		}
		if selected == nil || len(seen) != 2 || !seen["linux/amd64"] || !seen["linux/arm64"] {
			return empty, errors.New("complete canonical executable platform coverage required")
		}
		return *selected, nil
	}
	return empty, errors.New("canonical bootstrap executable component absent")
}

func validBootstrapTuple(component string, artifact ExecutableArtifact) bool {
	arch := "amd64"
	if artifact.Platform == "linux/arm64" {
		arch = "arm64"
	} else if artifact.Platform != "linux/amd64" {
		return false
	}
	if !contentSHA.MatchString(artifact.Digest) || artifact.SizeBytes <= 0 {
		return false
	}
	switch component {
	case "helm":
		name := "helm-v" + artifact.Version + "-linux-" + arch + ".tar.gz"
		return artifact.Kind == "tar.gz" && artifact.Name == name && artifact.URL == "https://get.helm.sh/"+name && artifact.ArchiveMember == "linux-"+arch+"/helm"
	case "github-cli":
		directory := "gh_" + artifact.Version + "_linux_" + arch
		name := directory + ".tar.gz"
		return artifact.Kind == "tar.gz" && artifact.Name == name && artifact.URL == "https://github.com/cli/cli/releases/download/v"+artifact.Version+"/"+name && artifact.ArchiveMember == directory+"/bin/gh"
	case "kubectl-shell":
		return artifact.Kind == "binary" && artifact.Name == "kubectl" && artifact.URL == "https://dl.k8s.io/release/v"+artifact.Version+"/bin/linux/"+arch+"/kubectl" && artifact.ArchiveMember == ""
	}
	return false
}

// A signed canonical kubectl-shell skew exception is retained for existing
// supported older Kubernetes lanes. This does not auto-adopt discovery1.37 or
// grant an arbitrary exception to Helm/GH; cluster compatibility is still an
// independent initial-observation and rehearsal requirement.
func bootstrapSelectionAllowed(component bootstrapComponent, now time.Time) bool {
	if component.Selected == nil {
		return false
	}
	if *component.Selected == component.Discovery.Version {
		return true
	}
	if component.ID != "kubectl-shell" || len(component.Exception) == 0 || string(component.Exception) == "null" {
		return false
	}
	var exception struct {
		Reason        string `json:"reason"`
		Owner         string `json:"owner"`
		Evidence      string `json:"evidence"`
		ReviewBy      string `json:"reviewBy"`
		ExitCondition string `json:"exitCondition"`
		CreatedAt     string `json:"createdAt"`
	}
	if jsoncontract.Decode(json.RawMessage(component.Exception), &exception) != nil || exception.Reason == "" || exception.Owner == "" || exception.Evidence == "" || exception.ExitCondition == "" {
		return false
	}
	review, err := time.Parse(time.RFC3339Nano, exception.ReviewBy)
	if err != nil || !review.After(now) {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, exception.CreatedAt)
	return err == nil && !created.After(now)
}
