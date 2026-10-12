//go:build linux

package distribution

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Execute the captured open inode, never reopen an operator-replaceable path.
// Only the protected producer uses this Linux adapter; ordinary verification
// on every shipped CLI platform remains unchanged.
func runPinnedDatabaseSigner(ctx context.Context, binary *os.File, args []string, qa bool, qaPassword string, github *DatabasePublicationGitHubRequest) error {
	if ctx == nil || binary == nil {
		return errors.New("database publisher pinned signer unavailable")
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	command.ExtraFiles = []*os.File{binary}
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.WaitDelay = 5 * time.Second
	// No registry token/store, ambient key, signing override or unrelated OIDC
	// provider is inherited. QA does not inherit GitHub signing credentials.
	environment, err := databasePublicationSignerEnvironment(qa, qaPassword, github)
	if err != nil {
		return err
	}
	command.Env = environment
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("database publisher local signer failed")
	}
	return ctx.Err()
}
