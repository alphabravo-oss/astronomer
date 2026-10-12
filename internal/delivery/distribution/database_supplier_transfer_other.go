//go:build !unix

package distribution

import (
	"errors"
	"os"
)

func openSupplierTransferDirectory(string, int64) (*os.File, os.FileInfo, error) {
	return nil, nil, errors.New("supplier database transfer platform unqualified")
}
