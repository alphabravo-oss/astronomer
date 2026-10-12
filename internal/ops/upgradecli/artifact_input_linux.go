//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// ExecuteArtifactInput streams an acknowledged retained private artifact through
// owned child stdin. It independently rechecks its bytes before and after joined
// success. Stderr and returned stdout keep the original diagnostic budget.
// This verifies retained-input integrity and the joined child result; the caller
// must establish actual consumption through archive list/restore/schema checks
// and independently verify fresh database mutation authority.
func (c *Commands) ExecuteArtifactInput(ctx context.Context, tool Tool, args []string, input *process.ArtifactCapture, maximum int64) ([]byte, process.Result, error) {
	if c == nil || ctx == nil {
		return nil, process.Result{}, errors.New("explicit private artifact input composition and context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, process.Result{}, err
	}
	if tool.Kind != Kubectl {
		return nil, process.Result{}, errors.New("private database artifact input requires bound kubectl")
	}
	c.mu.Lock()
	_, verified := c.verified[tool]
	c.mu.Unlock()
	if !verified {
		return nil, process.Result{}, errors.New("actual tool version preflight required")
	}
	if input == nil || maximum <= 0 {
		return nil, process.Result{}, errors.New("retained private artifact input and byte policy required")
	}
	copyInput := *input
	return c.executeBound(ctx, tool, args, process.ArtifactOutput{}, &copyInput, maximum)
}

func verifyArtifactInput(ctx context.Context, directory string, input *process.ArtifactCapture, maximum int64) error {
	if input == nil || filepath.Dir(input.Path) != directory {
		return errors.New("private input is outside the bound capture scope")
	}
	result := process.Result{Kind: process.Success, LeaderReaped: true, StdoutSHA256: input.SHA256, StdoutArtifact: input}
	return verifyArtifactCapture(ctx, directory, filepath.Base(input.Path), maximum, result)
}
