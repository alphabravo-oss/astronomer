//go:build !unix

package gitops

import (
	"errors"
	"os"
)

// Refuse this repository-read procedure without a qualified no-follow,
// nonblocking regular-file opener; never fall back to a path-based ReadFile.
func openCheckoutRegular(*os.Root, string) (*os.File, error) {
	return nil, errors.New("repository YAML inspection platform unqualified")
}

func checkoutSingleRegular(os.FileInfo) bool { return false }
