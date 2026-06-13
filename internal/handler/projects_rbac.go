// Per-project RBAC matrix endpoint.
//
// GET /api/v1/projects/{id}/rbac/ returns the binding rows for one
// project, each enriched with the role's display name + permission
// summary and the principal's display name (user or group). The
// frontend renders this as a "Members & roles" table on the project
// detail page — the surface gap that made our projects look thin
// compared to Rancher's project page.
//
// Response shape:
//
//   {
//     "data": {
//       "project_id": "...",
//       "bindings": [
//         {
//           "id": "binding-uuid",
//           "principal_kind": "user" | "group",
//           "principal_id":   "user-uuid" | "",
//           "principal_name": "alice@example.com" | "sre-oncall",
//           "role_id":        "role-uuid",
//           "role_name":      "Project Admin",
//           "role_display":   "Admin",
//           "is_builtin":     true,
//           "created_at":     "2026-…"
//         }, …
//       ],
//       "summary": {
//         "user_count":  3,
//         "group_count": 1,
//         "role_counts": { "Project Admin": 2, "Project Viewer": 2 }
//       }
//     }
//   }
//
// Errors:
//   404 — project not found
//   500 — DB / role lookup failure (logged; client retries are safe)
//
// Lookup behaviour: role + user lookups are best-effort. A binding
// whose role has been deleted (FK ON DELETE RESTRICT prevents this
// in practice, but defensive) renders with role_name="<deleted role>".
// A user binding whose user was deleted (CASCADE drops the binding
// too, so this is mostly impossible) likewise renders gracefully
// rather than 500ing the whole matrix.

package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type rbacBindingRow struct {
	ID            string `json:"id"`
	PrincipalKind string `json:"principal_kind"`
	PrincipalID   string `json:"principal_id,omitempty"`
	PrincipalName string `json:"principal_name"`
	RoleID        string `json:"role_id"`
	RoleName      string `json:"role_name"`
	RoleDisplay   string `json:"role_display"`
	IsBuiltin     bool   `json:"is_builtin"`
	CreatedAt     string `json:"created_at"`
}

type rbacMatrixResponse struct {
	ProjectID string            `json:"project_id"`
	Bindings  []rbacBindingRow  `json:"bindings"`
	Summary   rbacMatrixSummary `json:"summary"`
}

type rbacMatrixSummary struct {
	UserCount  int            `json:"user_count"`
	GroupCount int            `json:"group_count"`
	RoleCounts map[string]int `json:"role_counts"`
}

// RBACMatrix handles GET /api/v1/projects/{id}/rbac/.
func (h *ProjectHandler) RBACMatrix(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid project ID")
		return
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Project not found")
		return
	}

	// Cap at 500 rows. A single project with more than ~500 bindings
	// is operationally pathological; the matrix UI would be unusable
	// at that size anyway. Operators with that scale should be using
	// group bindings + group sync instead of per-user rows.
	bindings, err := h.queries.ListProjectRoleBindingsByProject(r.Context(), sqlc.ListProjectRoleBindingsByProjectParams{
		ProjectID: projectID,
		Limit:     500,
		Offset:    0,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "list_error", "Failed to list bindings")
		return
	}

	rows := make([]rbacBindingRow, 0, len(bindings))
	summary := rbacMatrixSummary{RoleCounts: map[string]int{}}
	roleCache := map[uuid.UUID]sqlc.ProjectRole{}
	userCache := map[uuid.UUID]sqlc.User{}

	for _, b := range bindings {
		// Resolve role (cached — many bindings share the same role).
		role, ok := roleCache[b.RoleID]
		if !ok {
			role, err = h.queries.GetProjectRoleByID(r.Context(), b.RoleID)
			if err == nil {
				roleCache[b.RoleID] = role
			}
		}
		roleName := "<deleted role>"
		roleDisplay := roleName
		isBuiltin := false
		if role.ID != uuid.Nil {
			roleName = role.Name
			roleDisplay = role.DisplayName
			if roleDisplay == "" {
				roleDisplay = role.Name
			}
			isBuiltin = role.IsBuiltin
		}

		row := rbacBindingRow{
			ID:          b.ID.String(),
			RoleID:      b.RoleID.String(),
			RoleName:    roleName,
			RoleDisplay: roleDisplay,
			IsBuiltin:   isBuiltin,
			CreatedAt:   b.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}

		// Distinguish user-bound from group-bound rows. group_role_bindings
		// stores the group name in the `group` column with user_id NULL;
		// per-user bindings carry user_id and group="".
		if b.UserID.Valid {
			row.PrincipalKind = "user"
			uid := uuid.UUID(b.UserID.Bytes)
			row.PrincipalID = uid.String()
			if u, cached := userCache[uid]; cached {
				row.PrincipalName = userDisplayName(u)
			} else if u, err := h.queries.GetUserByID(r.Context(), uid); err == nil {
				userCache[uid] = u
				row.PrincipalName = userDisplayName(u)
			} else {
				row.PrincipalName = "<deleted user>"
			}
			summary.UserCount++
		} else {
			row.PrincipalKind = "group"
			row.PrincipalName = b.Group
			summary.GroupCount++
		}
		summary.RoleCounts[roleDisplay]++
		rows = append(rows, row)
	}

	RespondJSON(w, http.StatusOK, rbacMatrixResponse{
		ProjectID: projectID.String(),
		Bindings:  rows,
		Summary:   summary,
	})
}

// userDisplayName picks the most useful identifier for a user row
// (email preferred, falls back to username, then to UUID — UUID is
// the last resort so a malformed user row still produces a stable
// non-empty display).
func userDisplayName(u sqlc.User) string {
	if u.Email != "" {
		return u.Email
	}
	if u.Username != "" {
		return u.Username
	}
	return u.ID.String()
}

// (silence unused — context import is here in case future revisions
// need it; keeping the import slot prevents tedious adds/removes.)
var _ = context.Background
