package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
)

type scimGroupMutationResult struct {
	group      scimGroup
	status     int
	idempotent bool
}

// CreateGroup handles POST /scim/v2/Groups (DIR-03). Creates an
// identity_group_mappings row for the displayName so the group becomes
// visible to List/Get and to SSO group sync. Optional members[] are
// written into user_idp_groups.
func (h *SCIMHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var body scimGroupCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		h.scimError(w, http.StatusBadRequest, "displayName is required")
		return
	}
	result, err := executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimGroupMutationResult, error) {
		exists, existsErr := scimGroupExists(r.Context(), q, name)
		if existsErr != nil {
			return scimGroupMutationResult{}, existsErr
		}
		if exists {
			return scimGroupMutationResult{group: toSCIMGroup(name), status: http.StatusOK, idempotent: true}, nil
		}
		roleID, roleErr := resolveSCIMGroupRoleID(r.Context(), q, body.RoleID)
		if roleErr != nil {
			return scimGroupMutationResult{}, roleErr
		}
		if _, createErr := q.CreateGroupMapping(r.Context(), sqlc.CreateGroupMappingParams{
			GroupName: name, Scope: "global", RoleID: roleID,
		}); createErr != nil {
			return scimGroupMutationResult{}, createErr
		}
		for _, member := range body.Members {
			if memberErr := addUserToSCIMGroup(r.Context(), q, member.Value, name); memberErr != nil {
				return scimGroupMutationResult{}, memberErr
			}
		}
		return scimGroupMutationResult{group: toSCIMGroup(name), status: http.StatusCreated}, nil
	}, func(result scimGroupMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.group.create", resourceType: "scim_group",
			resourceID: result.group.ID, resourceName: result.group.DisplayName, status: result.status,
			detail: scimMutationDetail(r, map[string]any{"member_count": len(body.Members), "idempotent": result.idempotent}),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to create group mapping")
		return
	}
	h.writeSCIM(w, result.status, result.group)
}

// PatchGroup handles PATCH /scim/v2/Groups/{id} for displayName replace and
// members add/remove (DIR-03).
func (h *SCIMHandler) PatchGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "id")
	if name == "" {
		h.scimError(w, http.StatusNotFound, "group not found")
		return
	}
	var body scimGroupPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.scimError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	for _, schema := range body.Schemas {
		if schema != "" && schema != scimPatchSchema {
			h.scimError(w, http.StatusBadRequest, "unsupported schema")
			return
		}
	}
	result, err := executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimGroupMutationResult, error) {
		exists, existsErr := scimGroupExists(r.Context(), q, name)
		if existsErr != nil {
			return scimGroupMutationResult{}, existsErr
		}
		if !exists {
			return scimGroupMutationResult{}, errSCIMGroupNotFound
		}
		currentName := name
		for _, op := range body.Operations {
			opName := strings.ToLower(strings.TrimSpace(op.Op))
			path := strings.ToLower(strings.TrimSpace(op.Path))
			switch {
			case (opName == "replace" || opName == "add") && (path == "displayname" || path == ""):
				newName, parseErr := parseSCIMGroupDisplayName(path, op.Value)
				if parseErr != nil {
					return scimGroupMutationResult{}, parseErr
				}
				if newName != "" && newName != currentName {
					if renameErr := renameSCIMGroup(r.Context(), q, currentName, newName); renameErr != nil {
						return scimGroupMutationResult{}, renameErr
					}
					currentName = newName
				}
			case (opName == "add" || opName == "replace") && strings.HasPrefix(path, "members"):
				members, parseErr := parseSCIMGroupMembers(op.Value)
				if parseErr != nil {
					return scimGroupMutationResult{}, parseErr
				}
				for _, member := range members {
					if memberErr := addUserToSCIMGroup(r.Context(), q, member.Value, currentName); memberErr != nil {
						return scimGroupMutationResult{}, memberErr
					}
				}
			case opName == "remove" && strings.HasPrefix(path, "members"):
				members, parseErr := parseSCIMRemovedMembers(path, op.Value)
				if parseErr != nil {
					return scimGroupMutationResult{}, parseErr
				}
				for _, member := range members {
					if memberErr := removeUserFromSCIMGroup(r.Context(), q, member.Value, currentName); memberErr != nil {
						return scimGroupMutationResult{}, memberErr
					}
				}
			}
		}
		return scimGroupMutationResult{group: toSCIMGroup(currentName), status: http.StatusOK}, nil
	}, func(result scimGroupMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.group.update", resourceType: "scim_group",
			resourceID: result.group.ID, resourceName: result.group.DisplayName, status: result.status,
			detail: scimMutationDetail(r, map[string]any{"previous_name": name}),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to update group")
		return
	}
	h.writeSCIM(w, http.StatusOK, result.group)
}

// ListGroups handles GET /scim/v2/Groups. Each distinct group_name in
// identity_group_mappings becomes one SCIM Group resource. The Group id
// is the displayName (group names are unique in the SCIM view), so an
// IdP can GET it back directly.
func (h *SCIMHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	startIndex, count := scimPaging(r)
	names, err := h.queries.ListSCIMGroupNames(r.Context(), sqlc.ListSCIMGroupNamesParams{
		Limit:  int32(count),
		Offset: int32(startIndex - 1),
	})
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	total, err := h.queries.CountSCIMGroupNames(r.Context())
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to count groups")
		return
	}
	resources := make([]any, 0, len(names))
	for _, n := range names {
		resources = append(resources, toSCIMGroup(n))
	}
	h.writeSCIM(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: total,
		StartIndex:   startIndex,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}

// GetGroup handles GET /scim/v2/Groups/{id}, where {id} is the group's
// displayName (URL-escaped).
func (h *SCIMHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "id")
	// {id} is the group name; confirm it actually exists by scanning the
	// (small, operator-curated) set rather than adding another query.
	names, err := h.queries.ListSCIMGroupNames(r.Context(), sqlc.ListSCIMGroupNamesParams{
		Limit:  scimMaxListResult,
		Offset: 0,
	})
	if err != nil {
		h.scimError(w, http.StatusInternalServerError, "failed to look up group")
		return
	}
	for _, n := range names {
		if n == name {
			h.writeSCIM(w, http.StatusOK, toSCIMGroup(n))
			return
		}
	}
	h.scimError(w, http.StatusNotFound, "group not found")
}

// DeleteGroup handles DELETE /scim/v2/Groups/{id}, where {id} is the group's
// displayName. Removes every identity_group_mappings row for that name and
// strips the group from user_idp_groups membership lists. Returns 204 on
// success (RFC 7644) and 404 when the group is unknown.
func (h *SCIMHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "id")
	if name == "" {
		h.scimError(w, http.StatusNotFound, "group not found")
		return
	}
	_, err := executeMutation(r, h.runTx, func(q SCIMMutationTx) (scimGroupMutationResult, error) {
		exists, existsErr := scimGroupExists(r.Context(), q, name)
		if existsErr != nil {
			return scimGroupMutationResult{}, existsErr
		}
		if !exists {
			return scimGroupMutationResult{}, errSCIMGroupNotFound
		}
		if deleteErr := deleteSCIMGroup(r.Context(), q, name); deleteErr != nil {
			return scimGroupMutationResult{}, deleteErr
		}
		return scimGroupMutationResult{group: toSCIMGroup(name), status: http.StatusNoContent}, nil
	}, func(result scimGroupMutationResult) mutationAuditEvent {
		return mutationAuditEvent{
			action: "scim.group.delete", resourceType: "scim_group",
			resourceID: result.group.ID, resourceName: result.group.DisplayName, status: result.status,
			detail: scimMutationDetail(r, nil),
		}
	})
	if err != nil {
		h.handleMutationError(w, err, http.StatusInternalServerError, "failed to delete group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toSCIMGroup(name string) scimGroup {
	return scimGroup{
		Schemas:     []string{scimGroupSchema},
		ID:          name,
		DisplayName: name,
		Meta:        scimMeta{ResourceType: "Group"},
	}
}

// --- Discovery endpoints (read-only, static documents) ---
//
// Azure AD / Okta probe these before they ever provision. They are
// constant documents describing what this slice actually supports, so
// they are served from in-code literals rather than the DB. They sit
// under the same static-bearer Auth chain as the rest of /scim/v2/*.

const (
	scimServiceProviderConfigSchema = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	scimResourceTypeSchema          = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
)

// ServiceProviderConfig handles GET /scim/v2/ServiceProviderConfig
// (RFC 7643 §5). It advertises exactly what this slice implements:
// PATCH yes, filter yes (the userName-eq we support), bulk/sort/etag/
// changePassword no.
