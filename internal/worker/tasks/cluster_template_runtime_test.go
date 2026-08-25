package tasks

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestClusterTemplateRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (ClusterTemplateRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty cluster-template runtime validated successfully")
	}
	for _, dependency := range []string{"queries", "installer", "recovery_enqueuer"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}

	runtime := ClusterTemplateRuntime{
		Deps: ClusterTemplateApplyDeps{
			Queries: newFakeApplyQuerier(sqlc.Cluster{}, sqlc.ClusterTemplateApplication{}), Installer: &fakeInstaller{},
		},
		RecoveryEnqueuer: &fakeEnqueuer{},
	}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[ClusterTemplateApplyType] == nil || bindings[ClusterTemplateDriftCheckType] == nil {
		t.Fatalf("cluster-template bindings = %#v", bindings)
	}

	var typedNil *fakeApplyQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil query validation error = %v", err)
	}
}
