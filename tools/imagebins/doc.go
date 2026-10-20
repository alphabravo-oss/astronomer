// Package imagebins is a tool module for third-party Go binaries shipped in
// Astronomer images (age in astronomer-dr, kubectl in astronomer-shell). They
// are compiled here with the pinned golang image (deploy/docker/go-image.lock)
// instead of being copied from upstream release builds or Alpine packages, so
// every Go binary in every image is built with go.mod's Go version (>= 1.27.3).
// Kept out of the product module so their dependency graphs stay out of go.sum.
package imagebins

import (
	_ "filippo.io/age/cmd/age"
	_ "k8s.io/kubectl/pkg/cmd"
)
