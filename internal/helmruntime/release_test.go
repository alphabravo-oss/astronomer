package helmruntime

import (
	"errors"
	"testing"

	common "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
)

func TestHelmV1LayoutGuardPreservesRevisionAndRejectsUnknownRepresentation(t *testing.T) {
	expected := &releasev1.Release{Name: "fixture", Namespace: "owned", Version: 4, Info: &releasev1.Info{Status: common.StatusPendingRollback, Description: "owned operation marker"}}
	actual, err := ReleaseV1(expected)
	if err != nil || actual != expected || actual.Version != 4 || actual.Info.Description != expected.Info.Description {
		t.Fatal("v1 layout changed")
	}
	copy, err := ReleaseV1(*expected)
	if err != nil || copy.Version != expected.Version || copy.Info.Status != expected.Info.Status {
		t.Fatal("v1 value representation changed")
	}
	if _, err := ReleaseV1(struct{ Status string }{Status: "deployed"}); err == nil || errors.Is(err, ErrReleaseMetadataUnavailable) {
		t.Fatal("unknown representation treated as deployed or missing v1 info")
	}
	if _, err := ReleaseV1(&releasev1.Release{}); !errors.Is(err, ErrReleaseMetadataUnavailable) {
		t.Fatal("missing-info history distinction lost")
	}
	var missing *releasev1.Release
	if _, err := ReleaseV1(nil); !errors.Is(err, ErrReleaseMetadataUnavailable) {
		t.Fatal("nil history release must retain the missing-metadata distinction")
	}
	if _, err := ReleaseV1(missing); !errors.Is(err, ErrReleaseMetadataUnavailable) {
		t.Fatal("typed nil history release must retain the missing-metadata distinction")
	}
}
