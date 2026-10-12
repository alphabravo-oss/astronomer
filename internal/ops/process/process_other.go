//go:build !linux

package process

import (
	"context"
	"errors"
)

func runOwned(context.Context, Spec) (Result, error) {
	result := Result{Kind: Unsupported, ExitCode: 1, ChildExitCode: -1}
	return result, &Error{Result: result, cause: errors.New("owned process groups are not qualified on this platform")}
}
