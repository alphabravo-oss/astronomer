//go:build !unix

package distribution

import (
	"errors"
	"os"
)

func supplierRefreshCapacity(*os.File, int64) error {
	return errors.New("database refresh private capacity unsupported on this platform")
}
