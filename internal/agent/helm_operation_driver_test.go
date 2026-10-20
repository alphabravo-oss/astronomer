package agent

import (
	"testing"

	"helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"

	"github.com/alphabravocompany/astronomer-go/internal/helmruntime"
)

func TestHelmOperationDriverPersistsMarkerAtRevisionCreation(t *testing.T) {
	store := driver.NewMemory()
	d := &helmOperationDriver{Driver: store, description: "astronomer tool operation op release 1"}
	rel := &release.Release{Name: "istiod", Namespace: "istio-system", Version: 4, Info: &release.Info{Status: common.StatusPendingRollback, Description: "Rollback to 2"}}
	if err := d.Create("istiod.v4", rel); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get("istiod.v4")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := helmruntime.ReleaseV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Info.Description != d.description || stored.Info.Status != common.StatusPendingRollback || stored.Version != 4 {
		t.Fatalf("first persisted revision lost execution identity: %+v", stored)
	}
}
