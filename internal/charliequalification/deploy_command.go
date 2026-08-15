package charliequalification

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// CommandCandidateDeployer invokes one operator-installed executable with only
// the validated immutable candidate. It deliberately has no shell, arbitrary
// environment, values, namespace, registry, or URL input from the request.
type CommandCandidateDeployer struct {
	path    string
	timeout time.Duration
}

func NewCommandCandidateDeployer(path string, timeout time.Duration) (*CommandCandidateDeployer, error) {
	if !filepath.IsAbs(path) || timeout < time.Minute || timeout > 20*time.Minute {
		return nil, errors.New("candidate deploy command requires an absolute path and a one-to-twenty-minute timeout")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("candidate deploy command must be an executable regular file not writable by group or other")
	}
	return &CommandCandidateDeployer{path: path, timeout: timeout}, nil
}

func (d *CommandCandidateDeployer) Deploy(ctx context.Context, candidate Candidate) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	command := exec.CommandContext(ctx, d.path,
		"--ref", candidate.Ref,
		"--commit", candidate.Commit,
		"--version", candidate.Version,
		"--central-image-digest", candidate.CentralImageDigest,
		"--agent-image-digest", candidate.AgentImageDigest,
		"--central-chart-digest", candidate.CentralChartDigest,
		"--agent-chart-digest", candidate.AgentChartDigest,
	)
	// Operator scripts can touch one-time credentials. Their output is never
	// reflected into HTTP responses or the qualification process log.
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return errors.New("fixed candidate deploy command failed")
	}
	return nil
}
