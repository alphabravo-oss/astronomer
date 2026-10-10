//go:build !linux

package distribution

import (
	"context"
	"errors"
	"os"
)

func runPinnedDatabaseSigner(context.Context, *os.File, []string, bool, string, *DatabasePublicationGitHubRequest) error {
	return errors.New("database publisher pinned executable is not qualified on this platform")
}
