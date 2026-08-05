// Package charlie contains the Astronomer-owned side of the optional Charlie
// integration. It must not import a Charlie central transport: runtime traffic
// is mediated only by the local Product Bridge client.
package charlie

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	AutomationUsername = "system:charlie-automation"
	AutomationEmail    = "system+charlie-automation@astronomer.invalid"
)

type automationIdentityQuerier interface {
	GetUserByUsername(context.Context, string) (sqlc.User, error)
	CreateServiceUser(context.Context, sqlc.CreateServiceUserParams) (sqlc.User, error)
	GetCharlieAutomationRole(context.Context) (sqlc.GlobalRole, error)
	EnsureCharlieAutomationBinding(context.Context, sqlc.EnsureCharlieAutomationBindingParams) (sqlc.GlobalRoleBinding, error)
}

// EnsureAutomationIdentity creates the hidden service principal and binds only
// the inert Charlie session permission template. It deliberately creates no API
// token and no target-resource grant. Operators must add named management-plane
// grants separately before auto mode can authorize an action.
func EnsureAutomationIdentity(ctx context.Context, q automationIdentityQuerier) (sqlc.User, error) {
	if q == nil {
		return sqlc.User{}, fmt.Errorf("charlie automation identity store is unavailable")
	}

	user, err := q.GetUserByUsername(ctx, AutomationUsername)
	if err != nil || !user.IsActive || !user.IsService {
		user, err = q.CreateServiceUser(ctx, sqlc.CreateServiceUserParams{
			Email:    AutomationEmail,
			Username: AutomationUsername,
		})
		if err != nil {
			return sqlc.User{}, fmt.Errorf("ensure Charlie automation service user: %w", err)
		}
	}
	if !user.IsService || !user.IsActive || user.IsSuperuser || user.IsStaff {
		return sqlc.User{}, fmt.Errorf("Charlie automation identity has unsafe user flags")
	}

	role, err := q.GetCharlieAutomationRole(ctx)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("load Charlie automation role: %w", err)
	}
	if err := validateAutomationRole(role); err != nil {
		return sqlc.User{}, err
	}

	if _, err := q.EnsureCharlieAutomationBinding(ctx, sqlc.EnsureCharlieAutomationBindingParams{
		UserID: pgtype.UUID{Bytes: user.ID, Valid: true},
		RoleID: role.ID,
	}); err != nil {
		return sqlc.User{}, fmt.Errorf("bind Charlie automation role: %w", err)
	}
	return user, nil
}

func validateAutomationRole(role sqlc.GlobalRole) error {
	if role.Name != "Charlie Automation" || !role.IsBuiltin {
		return fmt.Errorf("Charlie automation role identity is invalid")
	}
	var rules []struct {
		Resource string   `json:"resource"`
		Verbs    []string `json:"verbs"`
	}
	if err := json.Unmarshal(role.Rules, &rules); err != nil {
		return fmt.Errorf("decode Charlie automation role: %w", err)
	}
	if len(rules) != 1 || rules[0].Resource != "charlie" || !sameStrings(rules[0].Verbs, []string{"create", "read"}) {
		return fmt.Errorf("Charlie automation role must contain only charlie:create/read; refusing wildcard or target grants")
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	wanted := make(map[string]struct{}, len(right))
	for _, value := range right {
		wanted[value] = struct{}{}
	}
	for _, value := range left {
		if _, ok := wanted[value]; !ok {
			return false
		}
	}
	return true
}
