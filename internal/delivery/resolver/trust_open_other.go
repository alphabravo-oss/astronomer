//go:build !unix

package resolver

import "os"

const strictTrustOpeningAvailable = false

// Preserve the existing ordinary verifier contract on other platforms. The
// optional database physical-authority constructor refuses these platforms
// before this fallback; no bounded FIFO-open claim is made for ordinary use.
func openTrustRegular(path string) (*os.File, error) {
	return os.Open(path)
}
