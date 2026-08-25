package tasks

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

func TestProjectRuntimeValidatesAndBindsFamily(t *testing.T) {
	err := (ProjectRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty project runtime validated successfully")
	}
	for _, dependency := range []string{"queries", "requester", "encryptor"} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	runtime := ProjectRuntime{Deps: ProjectReconcileDeps{
		Queries: &fakeProjectQuerier{}, Requester: &fakeProjectRequester{}, Encryptor: encryptor,
	}}
	bindings, err := runtime.HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[ProjectReconcileType] == nil || bindings[ProjectReconcileAllType] == nil {
		t.Fatalf("project bindings = %#v", bindings)
	}
	var typedNil *fakeProjectQuerier
	runtime.Deps.Queries = typedNil
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "queries") {
		t.Fatalf("typed-nil validation error = %v", err)
	}
}
