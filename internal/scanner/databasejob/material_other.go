//go:build !linux

package databasejob

import (
	"context"
	"errors"
)

// Opt-in owned Job material copying requires the qualified Linux descriptor
// path. Ordinary distribution and legacy scanner platform behavior is unchanged.
func copyProjectedPreparationMaterial(context.Context, string, string, int, bool, func()) error {
	return errors.New("owned Job material platform unqualified")
}

func copyProjectedPreparationMaterialWithMapping(context.Context, string, string, int, bool, bool, func()) error {
	return errors.New("owned Job material platform unqualified")
}
