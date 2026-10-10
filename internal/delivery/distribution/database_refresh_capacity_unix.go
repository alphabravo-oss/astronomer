//go:build unix

package distribution

import (
	"os"
)

func supplierRefreshCapacity(directory *os.File, size int64) error {
	return supplierTransferCapacity(int(directory.Fd()), size)
}
