package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func scimGroupExists(ctx context.Context, q SCIMQuerier, name string) (bool, error) {
	return q.SCIMGroupExists(ctx, name)
}

func resolveSCIMGroupRoleID(ctx context.Context, q SCIMQuerier, explicit string) (uuid.UUID, error) {
	if explicit != "" {
		id, err := uuid.Parse(explicit)
		if err != nil {
			return uuid.Nil, fmt.Errorf("%w: invalid roleId", errSCIMInvalidGroupRole)
		}
		return id, nil
	}
	roles, err := q.ListGlobalRoles(ctx, sqlc.ListGlobalRolesParams{Limit: 200, Offset: 0})
	if err != nil {
		return uuid.Nil, err
	}
	for _, preferred := range []string{"Auditor", "Audit Viewer", "User"} {
		for _, role := range roles {
			if role.Name == preferred {
				return role.ID, nil
			}
		}
	}
	if len(roles) == 0 {
		return uuid.Nil, fmt.Errorf("%w: no global roles available to map SCIM group", errSCIMInvalidGroupRole)
	}
	return roles[0].ID, nil
}

func listAllSCIMGroupMappings(ctx context.Context, q SCIMQuerier) ([]sqlc.IdentityGroupMapping, error) {
	var out []sqlc.IdentityGroupMapping
	for offset := int32(0); ; offset += scimMaxListResult {
		rows, err := q.ListGroupMappings(ctx, sqlc.ListGroupMappingsParams{Limit: scimMaxListResult, Offset: offset})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(rows) < scimMaxListResult {
			return out, nil
		}
	}
}

func listAllSCIMUsers(ctx context.Context, q SCIMQuerier) ([]sqlc.User, error) {
	var out []sqlc.User
	for offset := int32(0); ; offset += scimMaxListResult {
		users, err := q.ListUsers(ctx, sqlc.ListUsersParams{Limit: scimMaxListResult, Offset: offset})
		if err != nil {
			return nil, err
		}
		out = append(out, users...)
		if len(users) < scimMaxListResult {
			return out, nil
		}
	}
}

func renameSCIMGroup(ctx context.Context, q SCIMMutationTx, oldName, newName string) error {
	if exists, err := scimGroupExists(ctx, q, newName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: displayName already exists", errSCIMInvalidPatch)
	}
	rows, err := listAllSCIMGroupMappings(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.GroupName != oldName {
			continue
		}
		if err := q.DeleteGroupMapping(ctx, row.ID); err != nil {
			return err
		}
		if _, err := q.CreateGroupMapping(ctx, sqlc.CreateGroupMappingParams{
			ConnectorID: row.ConnectorID,
			GroupName:   newName,
			Scope:       row.Scope,
			RoleID:      row.RoleID,
			ClusterID:   row.ClusterID,
			ProjectID:   row.ProjectID,
		}); err != nil {
			return err
		}
	}
	users, err := listAllSCIMUsers(ctx, q)
	if err != nil {
		return err
	}
	for _, user := range users {
		if err := replaceUserSCIMGroup(ctx, q, user.ID, oldName, newName); err != nil {
			return err
		}
	}
	return nil
}

func addUserToSCIMGroup(ctx context.Context, q SCIMMutationTx, userIDStr, groupName string) error {
	uid, err := uuid.Parse(strings.TrimSpace(userIDStr))
	if err != nil {
		return fmt.Errorf("%w: invalid member id", errSCIMInvalidPatch)
	}
	if _, err := q.GetUserByIDForUpdate(ctx, uid); errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: member not found", errSCIMInvalidPatch)
	} else if err != nil {
		return err
	}
	groups := []string{}
	row, err := q.GetUserIDPGroups(ctx, uid)
	if err == nil {
		if err := json.Unmarshal(row.Groups, &groups); err != nil {
			return err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	for _, group := range groups {
		if group == groupName {
			return nil
		}
	}
	groups = append(groups, groupName)
	raw, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	_, err = q.UpsertUserIDPGroups(ctx, sqlc.UpsertUserIDPGroupsParams{
		UserID: uid, Groups: raw, SyncedAt: time.Now().UTC(),
	})
	return err
}

func removeUserFromSCIMGroup(ctx context.Context, q SCIMMutationTx, userIDStr, groupName string) error {
	uid, err := uuid.Parse(strings.TrimSpace(userIDStr))
	if err != nil {
		return fmt.Errorf("%w: invalid member id", errSCIMInvalidPatch)
	}
	return replaceUserSCIMGroup(ctx, q, uid, groupName, "")
}

func replaceUserSCIMGroup(ctx context.Context, q SCIMMutationTx, uid uuid.UUID, oldName, newName string) error {
	if _, err := q.GetUserByIDForUpdate(ctx, uid); errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: member not found", errSCIMInvalidPatch)
	} else if err != nil {
		return err
	}
	row, err := q.GetUserIDPGroups(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var groups []string
	if err := json.Unmarshal(row.Groups, &groups); err != nil {
		return err
	}
	out := make([]string, 0, len(groups))
	changed := false
	for _, group := range groups {
		if group == oldName {
			changed = true
			if newName != "" {
				out = append(out, newName)
			}
			continue
		}
		out = append(out, group)
	}
	if !changed {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = q.UpsertUserIDPGroups(ctx, sqlc.UpsertUserIDPGroupsParams{
		UserID: uid, Groups: raw, SyncedAt: time.Now().UTC(),
	})
	return err
}

func parseSCIMGroupDisplayName(path string, raw json.RawMessage) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: invalid displayName value", errSCIMInvalidPatch)
	}
	switch typed := value.(type) {
	case string:
		if path != "displayname" {
			return "", nil
		}
		return strings.TrimSpace(typed), nil
	case map[string]any:
		displayName, _ := typed["displayName"].(string)
		return strings.TrimSpace(displayName), nil
	default:
		return "", fmt.Errorf("%w: invalid displayName value", errSCIMInvalidPatch)
	}
}

func parseSCIMGroupMembers(raw json.RawMessage) ([]scimGroupMember, error) {
	var members []scimGroupMember
	if err := json.Unmarshal(raw, &members); err == nil {
		return members, nil
	}
	var member scimGroupMember
	if err := json.Unmarshal(raw, &member); err != nil || strings.TrimSpace(member.Value) == "" {
		return nil, fmt.Errorf("%w: invalid members value", errSCIMInvalidPatch)
	}
	return []scimGroupMember{member}, nil
}

func parseSCIMRemovedMembers(path string, raw json.RawMessage) ([]scimGroupMember, error) {
	if strings.Contains(path, "value eq") {
		start := strings.Index(path, `"`)
		end := strings.LastIndex(path, `"`)
		if start < 0 || end <= start {
			return nil, fmt.Errorf("%w: invalid member filter", errSCIMInvalidPatch)
		}
		return []scimGroupMember{{Value: path[start+1 : end]}}, nil
	}
	return parseSCIMGroupMembers(raw)
}

// deleteSCIMGroup drops all mappings for groupName and removes the name from
// every user_idp_groups row that still lists it. The caller supplies one
// transaction-bound querier so visibility and membership cannot diverge.
func deleteSCIMGroup(ctx context.Context, q SCIMMutationTx, groupName string) error {
	rows, err := listAllSCIMGroupMappings(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.GroupName != groupName {
			continue
		}
		if err := q.DeleteGroupMapping(ctx, row.ID); err != nil {
			return err
		}
	}
	users, err := listAllSCIMUsers(ctx, q)
	if err != nil {
		return err
	}
	for _, user := range users {
		if err := removeUserFromSCIMGroup(ctx, q, user.ID.String(), groupName); err != nil {
			return err
		}
	}
	return nil
}
