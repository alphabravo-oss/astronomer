//go:build !unix

package distribution

import (
	"errors"
	"os"
)

// A platform without a qualified no-follow/nonblocking regular-file opener
// refuses this operator procedure rather than falling back to blocking open.
func openDatabaseInspectionRegular(string) (*os.File, error) {
	return nil, errors.New("database payload inspection platform unqualified")
}
