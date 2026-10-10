package helmruntime

import (
	"context"
	"errors"

	"helm.sh/helm/v4/pkg/release"
)

// RunJoined prevents the SDK install/upgrade control select from returning
// while its mutation worker is still running. Storage, transport and wait
// adapters must independently receive owner; suppressing control cancellation
// does not replace those bindings. The caller retains owner until return.
func RunJoined(owner context.Context, run func(context.Context) (release.Releaser, error)) (release.Releaser, error) {
	if owner == nil || run == nil {
		return nil, errors.New("owned Helm action and runner are required")
	}
	if err := owner.Err(); err != nil {
		return nil, err
	}
	result, err := run(context.WithoutCancel(owner))
	return result, errors.Join(err, owner.Err())
}
