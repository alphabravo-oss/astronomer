//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// HelmRecovery retains the previous revision's private values and manifests,
// history and release-scoped Secrets. It never cleans recovery material. This
// is one part of Backup, not signer/chart, readable PostgreSQL archive, target
// compatibility, complete checksum inventory or full Driver qualification.
// Repeated release observations are not an atomic lock against another owner.
type HelmRecovery struct {
	observation *HelmStateObservation
	policy      ArtifactPolicy
	mu          sync.Mutex
	attempted   bool
	artifacts   map[string]process.ArtifactCapture
}

func NewHelmRecovery(observation *HelmStateObservation, policy ArtifactPolicy) (*HelmRecovery, error) {
	if observation == nil || observation.release == nil || policy.MaximumBytes <= 0 {
		return nil, errors.New("initially bound release observation and positive recovery artifact budget required")
	}
	return &HelmRecovery{observation: observation, policy: policy, artifacts: make(map[string]process.ArtifactCapture)}, nil
}

// Capture is not replayable on this instance after any attempt. Failed stdout,
// copies and acknowledged earlier files remain private for explicit assessment.
// Exact legacy file names are exclusive copies; original process captures remain
// separately retained. A caller must not publish Backup before all other gates.
func (b *HelmRecovery) Capture(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("explicit private recovery composition and context required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.attempted {
		return errors.New("recovery attempt already retained; explicit assessment required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.attempted = true
	if err := b.original(ctx); err != nil {
		return err
	}
	r := b.observation.release
	revision := strconv.FormatUint(r.fence.options.Revision, 10)
	helm := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--kube-context", r.plan.Identity.Context}
	kube := []string{"--kubeconfig", r.fence.options.Kubeconfig, "--context", r.plan.Identity.Context}
	rows := []struct {
		name string
		tool Tool
		args []string
	}{
		{"values-all.yaml", r.tool, append(append([]string(nil), helm...), "get", "values", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace, "--revision", revision, "--all", "--output", "yaml")},
		{"values-user.yaml", r.tool, append(append([]string(nil), helm...), "get", "values", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace, "--revision", revision, "--output", "yaml")},
		{"manifest-before.yaml", r.tool, append(append([]string(nil), helm...), "get", "manifest", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace, "--revision", revision)},
		{"history.json", r.tool, append(append([]string(nil), helm...), "history", r.plan.Identity.Release, "--namespace", r.plan.Identity.Namespace, "--output", "json")},
		{"release-secrets.yaml", r.fence.kubectl, append(append([]string(nil), kube...), "get", "secrets", "--namespace", r.plan.Identity.Namespace, "-l", "app.kubernetes.io/instance="+r.plan.Identity.Release, "--output", "yaml")},
	}
	for _, row := range rows {
		if err := b.original(ctx); err != nil {
			return err
		}
		capture, _, err := r.fence.commands.captureRecovery(ctx, row.tool, row.args, b.policy)
		if err != nil {
			return err
		}
		if err := r.fence.commands.artifactCapacity(r.fence.commands.options.CaptureDirectory, b.policy.MaximumBytes); err != nil {
			return err
		}
		alias, err := copyRecoveryArtifact(ctx, r.fence.commands.options.CaptureDirectory, row.name, *capture, b.policy.MaximumBytes)
		if err != nil {
			return err
		}
		b.artifacts[row.name] = alias
		if err := b.original(ctx); err != nil {
			return err
		}
	}
	return b.verify(ctx)
}

// Verify performs fresh input/owner/release and file bindings before the higher
// Driver uses this recovery set. No bool or caller-provided digest grants proof.
func (b *HelmRecovery) Verify(ctx context.Context) error {
	if b == nil || ctx == nil {
		return errors.New("explicit recovery validation context required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.verify(ctx)
}

func (b *HelmRecovery) verify(ctx context.Context) error {
	if err := b.original(ctx); err != nil {
		return err
	}
	if len(b.artifacts) != 5 {
		return errors.New("previous-release recovery set incomplete")
	}
	directory := b.observation.release.fence.commands.options.CaptureDirectory
	for _, name := range []string{"values-all.yaml", "values-user.yaml", "manifest-before.yaml", "history.json", "release-secrets.yaml"} {
		capture, ok := b.artifacts[name]
		if !ok {
			return errors.New("previous-release recovery artifact missing")
		}
		if err := verifyArtifactInput(ctx, directory, &capture, b.policy.MaximumBytes); err != nil {
			return err
		}
	}
	return b.original(ctx)
}

func (b *HelmRecovery) original(ctx context.Context) error {
	state, err := b.observation.Read(ctx)
	if err != nil {
		return err
	}
	r := b.observation.release
	if state.Revision != r.fence.options.Revision || state.Status != "deployed" || !releaseVersion(state, r.plan.Previous) {
		return errors.New("original deployed revision changed during recovery capture")
	}
	return ctx.Err()
}

// Public CaptureArtifact remains restricted to the database adapter. Only this
// private recovery stage admits the already-verified bound Helm executable.
func (c *Commands) captureRecovery(ctx context.Context, tool Tool, args []string, policy ArtifactPolicy) (*process.ArtifactCapture, process.Result, error) {
	if tool.Kind == Kubectl {
		return c.CaptureArtifact(ctx, tool, args, policy)
	}
	if c == nil || ctx == nil || tool.Kind != Helm || policy.MaximumBytes <= 0 {
		return nil, process.Result{}, errors.New("bound Helm recovery and artifact policy required")
	}
	if err := ctx.Err(); err != nil {
		return nil, process.Result{}, err
	}
	c.mu.Lock()
	_, verified := c.verified[tool]
	c.mu.Unlock()
	if !verified || c.artifactCapacity == nil {
		return nil, process.Result{}, errors.New("actual Helm version and artifact capacity composition required")
	}
	if err := c.artifactCapacity(c.options.CaptureDirectory, policy.MaximumBytes); err != nil {
		return nil, process.Result{}, err
	}
	_, result, err := c.executeWithOutput(ctx, tool, args, process.ArtifactOutput{MaximumBytes: policy.MaximumBytes})
	if err != nil {
		return nil, result, err
	}
	capture := *result.StdoutArtifact
	return &capture, result, nil
}

// Copies are private, finite, context-bound and exclusive. A failed partial copy
// remains retained. No diagnostic budget increase or alias to original files.
func copyRecoveryArtifact(ctx context.Context, directory, name string, input process.ArtifactCapture, maximum int64) (process.ArtifactCapture, error) {
	var empty process.ArtifactCapture
	if ctx == nil || filepath.Base(name) != name || strings.ContainsAny(name, "\t\r\n") || name == "." {
		return empty, errors.New("exact private recovery file name required")
	}
	if err := verifyArtifactInput(ctx, directory, &input, maximum); err != nil {
		return empty, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return empty, errors.New("open private recovery scope")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	before, err := root.Lstat(filepath.Base(input.Path))
	if err != nil {
		return empty, errors.New("read original private capture identity")
	}
	source, err := root.OpenFile(filepath.Base(input.Path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return empty, errors.New("open original private capture")
	}
	defer func(close func() error) { _ = close() }(source.Close)
	opened, err := source.Stat()
	if err != nil || !sameArtifact(before, opened) {
		return empty, errors.New("original private capture changed before copy")
	}
	target, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return empty, errors.New("create exclusive private recovery copy")
	}
	defer func(close func() error) { _ = close() }(target.Close)
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		n, readErr := source.Read(buffer)
		if int64(n) > maximum-size {
			return empty, errors.New("private recovery copy budget exceeded")
		}
		if n > 0 {
			written, writeErr := target.Write(buffer[:n])
			if writeErr != nil || written != n {
				return empty, errors.New("write private recovery copy")
			}
			_, _ = hash.Write(buffer[:n])
			size += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil || n == 0 {
			return empty, errors.New("read private recovery copy")
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if size != input.SizeBytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != input.SHA256 {
		return empty, errors.New("copied private recovery bytes differ")
	}
	after, err := source.Stat()
	if err != nil || !sameArtifact(before, after) {
		return empty, errors.New("original private capture changed during copy")
	}
	named, err := root.Lstat(filepath.Base(input.Path))
	if err != nil || !sameArtifact(before, named) {
		return empty, errors.New("original private capture name changed during copy")
	}
	if err := target.Sync(); err != nil {
		return empty, errors.New("sync private recovery copy")
	}
	if err := target.Close(); err != nil {
		return empty, errors.New("close private recovery copy")
	}
	copied := process.ArtifactCapture{Path: filepath.Join(directory, name), SizeBytes: size, SHA256: input.SHA256, Acknowledged: true}
	if err := verifyArtifactInput(ctx, directory, &copied, maximum); err != nil {
		return empty, err
	}
	return copied, nil
}
