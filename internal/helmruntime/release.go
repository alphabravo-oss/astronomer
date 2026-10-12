package helmruntime

import (
	"errors"

	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
)

var ErrReleaseMetadataUnavailable = errors.New("Helm release metadata unavailable")

// ReleaseV1 preserves this application's existing chart-v2/release-v1 storage
// and history contract. Helm4's interface-valued results do not implicitly
// authorize chart-v3 or a different persisted release layout.
func ReleaseV1(value release.Releaser) (*releasev1.Release, error) {
	var result *releasev1.Release
	switch item := value.(type) {
	case nil:
		return nil, ErrReleaseMetadataUnavailable
	case *releasev1.Release:
		result = item
	case releasev1.Release:
		result = &item
	default:
		return nil, errors.New("unsupported Helm release representation")
	}
	if result == nil || result.Info == nil {
		return nil, ErrReleaseMetadataUnavailable
	}
	return result, nil
}
