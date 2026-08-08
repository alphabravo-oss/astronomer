package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type identityFake struct {
	user         sqlc.User
	lookupErr    error
	role         sqlc.GlobalRole
	created      int
	bindings     int
	bindingError error
}

func (f *identityFake) GetUserByUsername(context.Context, string) (sqlc.User, error) {
	return f.user, f.lookupErr
}

func (f *identityFake) CreateServiceUser(_ context.Context, arg sqlc.CreateServiceUserParams) (sqlc.User, error) {
	f.created++
	return sqlc.User{ID: uuid.New(), Email: arg.Email, Username: arg.Username, IsActive: true, IsService: true}, nil
}

func (f *identityFake) GetCharlieAutomationRole(context.Context) (sqlc.GlobalRole, error) {
	return f.role, nil
}

func (f *identityFake) EnsureCharlieAutomationBinding(context.Context, sqlc.EnsureCharlieAutomationBindingParams) (sqlc.GlobalRoleBinding, error) {
	f.bindings++
	return sqlc.GlobalRoleBinding{}, f.bindingError
}

func safeAutomationRole(t *testing.T) sqlc.GlobalRole {
	t.Helper()
	rules, err := json.Marshal([]map[string]any{{"resource": "charlie", "verbs": []string{"create", "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	return sqlc.GlobalRole{ID: uuid.New(), Name: "Charlie Automation", IsBuiltin: true, Rules: rules}
}

func TestEnsureAutomationIdentityCreatesNoCredentialAndBindsSafeRole(t *testing.T) {
	fake := &identityFake{lookupErr: errors.New("not found"), role: safeAutomationRole(t)}
	user, err := EnsureAutomationIdentity(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != AutomationUsername || !user.IsService || !user.IsActive {
		t.Fatalf("unsafe service user: %+v", user)
	}
	if fake.created != 1 || fake.bindings != 1 {
		t.Fatalf("created=%d bindings=%d, want 1/1", fake.created, fake.bindings)
	}
}

func TestEnsureAutomationIdentityReusesSafeServiceUser(t *testing.T) {
	fake := &identityFake{
		user: sqlc.User{ID: uuid.New(), Username: AutomationUsername, IsActive: true, IsService: true},
		role: safeAutomationRole(t),
	}
	if _, err := EnsureAutomationIdentity(context.Background(), fake); err != nil {
		t.Fatal(err)
	}
	if fake.created != 0 || fake.bindings != 1 {
		t.Fatalf("created=%d bindings=%d, want 0/1", fake.created, fake.bindings)
	}
}

func TestEnsureAutomationIdentityRejectsPrivilegeWidening(t *testing.T) {
	for _, rules := range []string{
		`[{"resource":"*","verbs":["*"]}]`,
		`[{"resource":"charlie","verbs":["create","read"]},{"resource":"clusters","verbs":["update"]}]`,
		`[{"resource":"charlie","verbs":["create","read","manage"]}]`,
	} {
		fake := &identityFake{
			user: sqlc.User{ID: uuid.New(), Username: AutomationUsername, IsActive: true, IsService: true},
			role: sqlc.GlobalRole{ID: uuid.New(), Name: "Charlie Automation", IsBuiltin: true, Rules: json.RawMessage(rules)},
		}
		if _, err := EnsureAutomationIdentity(context.Background(), fake); err == nil {
			t.Fatalf("unsafe rules %s were accepted", rules)
		}
		if fake.bindings != 0 {
			t.Fatal("unsafe role was bound")
		}
	}
}

func TestEnsureAutomationIdentityRejectsHumanOrPrivilegedUser(t *testing.T) {
	for _, user := range []sqlc.User{
		{ID: uuid.New(), IsActive: true, IsService: true, IsSuperuser: true},
		{ID: uuid.New(), IsActive: true, IsService: true, IsStaff: true},
	} {
		fake := &identityFake{user: user, role: safeAutomationRole(t)}
		if _, err := EnsureAutomationIdentity(context.Background(), fake); err == nil {
			t.Fatal("privileged service identity was accepted")
		}
	}
}
