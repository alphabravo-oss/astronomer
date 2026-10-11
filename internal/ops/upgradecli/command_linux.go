//go:build linux

// Package upgradecli composes bound operator tools with the owned process and
// upgrade cores. The legacy shell remains authoritative until stage parity is
// qualified. This package does not infer authorization from ambient contexts.
package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

type ToolKind string

const (
	Helm      ToolKind = "helm"
	Kubectl   ToolKind = "kubectl"
	GitHubCLI ToolKind = "gh"
)

// Tool is supplied from exact canonical authority or a separately bound
// historical release qualification. The path never comes from PATH lookup.
// Version is checked against the actual child's version output, not a flag.
type Tool struct {
	Kind    ToolKind
	Path    string
	Version string
	SHA256  string
}

type CommandOptions struct {
	Directory           string
	CaptureDirectory    string
	Environment         []process.Environment
	Timeout             time.Duration
	TerminationGrace    time.Duration
	MaximumCaptureBytes int64
}

type commandCredential struct {
	path    string
	initial metadataInput
}

type Commands struct {
	credentials      []commandCredential
	options          CommandOptions
	mu               sync.Mutex
	sequence         uint16
	verified         map[Tool]struct{}
	run              func(context.Context, process.Spec) (process.Result, error)
	artifactCapacity func(string, int64) error
}

func NewCommands(options CommandOptions) (*Commands, error) {
	if !filepath.IsAbs(options.Directory) || !filepath.IsAbs(options.CaptureDirectory) || options.Timeout <= 0 || options.TerminationGrace <= 0 || options.TerminationGrace > 5*time.Second || options.MaximumCaptureBytes <= 0 || options.MaximumCaptureBytes > 16<<20 {
		return nil, errors.New("explicit command paths and finite lifecycle/capture bounds required")
	}
	options.Environment = append([]process.Environment(nil), options.Environment...)
	if len(options.Environment) > 64 {
		return nil, errors.New("finite named operator environment required")
	}
	credentials := make([]commandCredential, 0, len(options.Environment))
	for _, binding := range options.Environment {
		if binding.SecretFile == "" {
			continue
		}
		initial, err := bindMetadataArtifact(context.Background(), binding.SecretFile, 64<<10)
		if err != nil {
			return nil, errors.New("initial private operator credential unavailable")
		}
		credentials = append(credentials, commandCredential{binding.SecretFile, initial})
	}
	return &Commands{options: options, credentials: credentials, run: process.Run, artifactCapacity: artifactCapacity, verified: make(map[Tool]struct{})}, nil
}

// Execute retains raw output only in the process core's exclusive private
// captures. Returned stdout is bounded and hash-verified after owned pipes have
// joined. It must be decoded as a closed contract, never included in diagnostics.
// Executable identity is checked before and after each invocation. This does
// not claim atomic protection against a privileged concurrent tool replacement.
func (c *Commands) Execute(ctx context.Context, tool Tool, args []string) ([]byte, process.Result, error) {
	if c == nil {
		return nil, process.Result{}, errors.New("explicit command composition required")
	}
	c.mu.Lock()
	_, verified := c.verified[tool]
	c.mu.Unlock()
	if !verified {
		return nil, process.Result{}, errors.New("actual tool version preflight required")
	}
	return c.execute(ctx, tool, args)
}

func (c *Commands) execute(ctx context.Context, tool Tool, args []string) ([]byte, process.Result, error) {
	return c.executeWithOutput(ctx, tool, args, process.ArtifactOutput{})
}

func (c *Commands) executeWithOutput(ctx context.Context, tool Tool, args []string, artifact process.ArtifactOutput) ([]byte, process.Result, error) {
	return c.executeBound(ctx, tool, args, artifact, nil, 0)
}

func (c *Commands) executeBound(ctx context.Context, tool Tool, args []string, artifact process.ArtifactOutput, input *process.ArtifactCapture, inputMaximum int64) ([]byte, process.Result, error) {
	if ctx == nil || c == nil {
		return nil, process.Result{}, errors.New("explicit command context and composition required")
	}
	if err := ctx.Err(); err != nil {
		return nil, process.Result{}, err
	}
	if err := c.validateCredentialEnvironment(ctx); err != nil {
		return nil, process.Result{}, err
	}
	if input != nil {
		if err := verifyArtifactInput(ctx, c.options.CaptureDirectory, input, inputMaximum); err != nil {
			return nil, process.Result{}, err
		}
	}
	identity, err := toolIdentity(tool)
	if err != nil {
		return nil, process.Result{}, err
	}
	c.mu.Lock()
	if c.sequence >= 1000 {
		c.mu.Unlock()
		return nil, process.Result{}, errors.New("command count budget exhausted")
	}
	c.sequence++
	sequence := c.sequence
	c.mu.Unlock()
	spec := process.Spec{Executable: tool.Path, Args: append([]string(nil), args...), Environment: append([]process.Environment(nil), c.options.Environment...), Directory: c.options.Directory, CaptureDirectory: c.options.CaptureDirectory, ID: string(tool.Kind), Sequence: sequence, Timeout: c.options.Timeout, TerminationGrace: c.options.TerminationGrace, MaximumCaptureBytes: c.options.MaximumCaptureBytes, StdoutArtifact: artifact}
	if input != nil {
		spec.StdinFile = input.Path
	}
	result, runErr := c.run(ctx, spec)
	actual, identityErr := toolIdentity(tool)
	credentialErr := c.validateCredentialEnvironment(ctx)
	if credentialErr != nil {
		return nil, result, errors.Join(runErr, credentialErr)
	}
	if identityErr != nil || !os.SameFile(identity, actual) {
		return nil, result, errors.Join(runErr, errors.New("bound tool identity changed"))
	}
	if runErr != nil {
		return nil, result, runErr
	}
	if err := ctx.Err(); err != nil {
		return nil, result, err
	}
	if input != nil {
		if err := verifyArtifactInput(ctx, c.options.CaptureDirectory, input, inputMaximum); err != nil {
			return nil, result, err
		}
	}
	if result.Kind != process.Success || !result.LeaderReaped || result.StdoutSHA256 == "" {
		return nil, result, errors.New("owned command did not acknowledge joined success")
	}
	if artifact.MaximumBytes > 0 {
		if err := verifyArtifactCapture(ctx, c.options.CaptureDirectory, fmt.Sprintf("%s-%03d.stdout", spec.ID, sequence), artifact.MaximumBytes, result); err != nil {
			return nil, result, err
		}
		return nil, result, nil
	}
	name := fmt.Sprintf("%s-%03d.stdout", spec.ID, sequence)
	stdout, err := readCapture(c.options.CaptureDirectory, name, c.options.MaximumCaptureBytes, result.StdoutSHA256)
	return stdout, result, err
}

func (c *Commands) VerifyTool(ctx context.Context, tool Tool) error {
	args := []string{"version", "--template", "{{.Version}}"}
	if tool.Kind == Kubectl {
		args = []string{"version", "--client=true"}
	}
	if tool.Kind == GitHubCLI {
		args = []string{"version"}
	}
	raw, _, err := c.execute(ctx, tool, args)
	if err != nil {
		return err
	}
	version := strings.TrimSpace(string(raw))
	if tool.Kind == Kubectl {
		lines := strings.Split(version, "\n")
		if len(lines) < 1 || len(lines) > 2 || !strings.HasPrefix(lines[0], "Client Version: ") || (len(lines) == 2 && !strings.HasPrefix(lines[1], "Kustomize Version: ")) {
			return errors.New("kubectl client version output contract differs")
		}
		version = strings.TrimPrefix(lines[0], "Client Version: ")
	}
	if tool.Kind == GitHubCLI {
		actual, parseErr := githubToolVersion(version)
		if parseErr != nil {
			return parseErr
		}
		version = "v" + actual
	}
	if version != tool.Version {
		return errors.New("bound tool version differs")
	}
	c.mu.Lock()
	c.verified[tool] = struct{}{}
	c.mu.Unlock()
	return nil
}

func toolIdentity(tool Tool) (os.FileInfo, error) {
	if (tool.Kind != Helm && tool.Kind != Kubectl && tool.Kind != GitHubCLI) || !filepath.IsAbs(tool.Path) || tool.Version == "" || strings.ContainsAny(tool.Version, "\t\r\n ") || !strings.HasPrefix(tool.SHA256, "sha256:") || len(tool.SHA256) != 71 {
		return nil, errors.New("exact tool kind/path/version/digest binding required")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(tool.SHA256, "sha256:")); err != nil {
		return nil, errors.New("tool digest is not SHA256")
	}
	before, err := os.Lstat(tool.Path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0o022 != 0 || before.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("bound tool must be a regular executable without shared write permission")
	}
	fd, err := syscall.Open(tool.Path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("open bound tool")
	}
	file := os.NewFile(uintptr(fd), "bound-tool")
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() || opened.Size() > 512<<20 || opened.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("bound tool owner or identity differs")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, errors.New("read bound tool")
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != tool.SHA256 {
		return nil, errors.New("bound tool bytes differ")
	}
	return opened, nil
}

func readCapture(directory, name string, maximum int64, expected string) ([]byte, error) {
	parent, err := os.Lstat(directory)
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0o700 || parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("private capture parent differs")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, errors.New("open private capture scope")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	actualParent, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, actualParent) {
		return nil, errors.New("private capture parent identity changed")
	}
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() > maximum {
		return nil, errors.New("bounded private capture required")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("open private capture")
	}
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0o600 || opened.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("private capture identity differs")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, errors.New("private capture byte bound exceeded")
	}
	hash := sha256.Sum256(raw)
	if "sha256:"+hex.EncodeToString(hash[:]) != expected {
		return nil, errors.New("private capture bytes differ")
	}
	return raw, nil
}

func (c *Commands) validateCredentialEnvironment(ctx context.Context) error {
	if c == nil || ctx == nil {
		return errors.New("explicit operator credential composition required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, initial := range c.credentials {
		actual, err := bindMetadataArtifact(ctx, initial.path, 64<<10)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !sameArtifact(initial.initial.identity, actual.identity) || initial.initial.hash != actual.hash {
			return errors.New("initial operator credential file changed")
		}
	}
	return ctx.Err()
}
