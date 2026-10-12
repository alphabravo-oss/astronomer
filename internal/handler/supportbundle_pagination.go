package handler

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	supportSnapshotPageSize = 500
	supportSnapshotMaxPages = 10
	supportSnapshotMaxItems = supportSnapshotPageSize * supportSnapshotMaxPages
)

var errIncompleteSupportSnapshot = errors.New("incomplete Kubernetes snapshot")

// Every request retains the opaque continuation issued by the preceding page.
// The resource version is checked as response snapshot identity; it is never
// added to ListOptions alongside Continue. No partial data is returned as a
// complete snapshot on error, expiry, a repeated token, or an exhausted budget.
func collectSupportPages[T any](ctx context.Context, fetch func(context.Context, metav1.ListOptions) ([]T, metav1.ListMeta, error)) ([]T, error) {
	var items []T
	var token, version string
	seen := map[string]struct{}{}
	for page := 0; page < supportSnapshotMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: request stopped: %w", errIncompleteSupportSnapshot, err)
		}
		chunk, metadata, err := fetch(ctx, metav1.ListOptions{Limit: supportSnapshotPageSize, Continue: token})
		if stopped := ctx.Err(); stopped != nil {
			return nil, fmt.Errorf("%w: request stopped: %w", errIncompleteSupportSnapshot, stopped)
		}
		if err != nil {
			category := "list failed"
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) {
				category = "continuation expired"
			}
			return nil, fmt.Errorf("%w: %s", errIncompleteSupportSnapshot, category)
		}
		if page == 0 {
			version = metadata.ResourceVersion
		} else if metadata.ResourceVersion != version {
			return nil, fmt.Errorf("%w: snapshot changed", errIncompleteSupportSnapshot)
		}
		if len(chunk) > supportSnapshotPageSize {
			return nil, fmt.Errorf("%w: page size exceeded", errIncompleteSupportSnapshot)
		}
		if metadata.Continue != "" && metadata.ResourceVersion == "" {
			return nil, fmt.Errorf("%w: snapshot identity missing", errIncompleteSupportSnapshot)
		}
		if len(chunk) > supportSnapshotMaxItems-len(items) {
			return nil, fmt.Errorf("%w: item budget exhausted", errIncompleteSupportSnapshot)
		}
		items = append(items, chunk...)
		if metadata.Continue == "" {
			return items, nil
		}
		if _, exists := seen[metadata.Continue]; exists {
			return nil, fmt.Errorf("%w: continuation repeated", errIncompleteSupportSnapshot)
		}
		seen[metadata.Continue] = struct{}{}
		token = metadata.Continue
	}
	return nil, fmt.Errorf("%w: page budget exhausted", errIncompleteSupportSnapshot)
}
