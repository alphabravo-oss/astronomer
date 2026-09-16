package agent

import (
	"testing"

	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

func TestHelmOperationDriverPersistsMarkerAtRevisionCreation(t *testing.T) {
	store := driver.NewMemory()
	d := &helmOperationDriver{Driver: store, description: "astronomer tool operation op release 1"}
	rel := &release.Release{Name: "istiod", Namespace: "istio-system", Version: 4, Info: &release.Info{Status: release.StatusPendingRollback, Description: "Rollback to 2"}}
	if err := d.Create("istiod.v4", rel); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get("istiod.v4")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Info.Description != d.description || stored.Info.Status != release.StatusPendingRollback || stored.Version != 4 {
		t.Fatalf("first persisted revision lost execution identity: %+v", stored)
	}
}
