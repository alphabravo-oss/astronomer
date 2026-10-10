//go:build unix

package gitops

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openCheckoutRegular(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}

func checkoutSingleRegular(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	metadata, ok := info.Sys().(*syscall.Stat_t)
	return ok && metadata.Nlink == 1
}
