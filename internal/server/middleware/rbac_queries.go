package middleware

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// SQLCRBACQuerier adapts sqlc queries to the RBAC middleware binding interface.
type SQLCRBACQuerier struct {
	queries *sqlc.Queries
}

func NewSQLCRBACQuerier(queries *sqlc.Queries) *SQLCRBACQuerier {
	if queries == nil {
		return nil
	}
	return &SQLCRBACQuerier{queries: queries}
}

func (q *SQLCRBACQuerier) GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error) {
	if q == nil || q.queries == nil {
		return nil, nil
	}

	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}
	pgUserID := pgtype.UUID{Bytes: parsedUserID, Valid: true}

	// Superuser short-circuit: a single synthetic binding with IsSuperuser=true
	// causes the engine to grant any permission without consulting role data.
	if user, err := q.queries.GetUserByID(ctx, parsedUserID); err == nil && user.IsSuperuser {
		return []rbac.RoleBinding{{UserID: userID, IsSuperuser: true}}, nil
	}

	globalBindings, err := q.queries.GetGlobalRoleBindingsByUserID(ctx, pgUserID)
	if err != nil {
		return nil, err
	}
	clusterBindings, err := q.queries.GetClusterRoleBindingsByUserID(ctx, pgUserID)
	if err != nil {
		return nil, err
	}
	projectBindings, err := q.queries.GetProjectRoleBindingsByUserID(ctx, pgUserID)
	if err != nil {
		return nil, err
	}

	results := make([]rbac.RoleBinding, 0, len(globalBindings)+len(clusterBindings)+len(projectBindings))

	for _, binding := range globalBindings {
		role, err := q.queries.GetGlobalRoleByID(ctx, binding.RoleID)
		if err != nil {
			return nil, err
		}
		rules, err := decodeRoleRules(role.Rules)
		if err != nil {
			return nil, err
		}
		results = append(results, rbac.RoleBinding{
			UserID:    userID,
			Group:     binding.Group,
			RoleRules: rules,
		})
	}

	for _, binding := range clusterBindings {
		role, err := q.queries.GetClusterRoleByID(ctx, binding.RoleID)
		if err != nil {
			return nil, err
		}
		rules, err := decodeRoleRules(role.Rules)
		if err != nil {
			return nil, err
		}
		results = append(results, rbac.RoleBinding{
			UserID:    userID,
			Group:     binding.Group,
			RoleRules: rules,
			ClusterID: binding.ClusterID.String(),
		})
	}

	for _, binding := range projectBindings {
		role, err := q.queries.GetProjectRoleByID(ctx, binding.RoleID)
		if err != nil {
			return nil, err
		}
		rules, err := decodeRoleRules(role.Rules)
		if err != nil {
			return nil, err
		}
		results = append(results, rbac.RoleBinding{
			UserID:    userID,
			Group:     binding.Group,
			RoleRules: rules,
			ProjectID: binding.ProjectID.String(),
		})
	}

	return results, nil
}

func decodeRoleRules(raw json.RawMessage) ([]rbac.Rule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rules []rbac.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}
