package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (h *SCIMHandler) ServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	h.writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":          []string{scimServiceProviderConfigSchema},
		"documentationUri": "https://datatracker.ietf.org/doc/html/rfc7644",
		"patch":            map[string]any{"supported": true},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		// We only implement `userName eq "x"`, but filter is advertised as
		// supported because IdPs gate the pre-create lookup on this flag.
		"filter": map[string]any{"supported": true, "maxResults": scimMaxListResult},
		// DIR-03: Groups support create + patch (membership / displayName).
		"changePassword": map[string]any{"supported": false},
		"sort":           map[string]any{"supported": false},
		"etag":           map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{
			{
				"type":        "oauthbearertoken",
				"name":        "OAuth Bearer Token",
				"description": "Authentication via the static SCIM bearer token.",
				"primary":     true,
			},
		},
		"meta": map[string]any{"resourceType": "ServiceProviderConfig"},
	})
}

// ResourceTypes handles GET /scim/v2/ResourceTypes (RFC 7643 §6): the
// User and Group resources this provider exposes.
func (h *SCIMHandler) ResourceTypes(w http.ResponseWriter, r *http.Request) {
	resources := []any{
		map[string]any{
			"schemas":     []string{scimResourceTypeSchema},
			"id":          "User",
			"name":        "User",
			"endpoint":    "/Users",
			"description": "User Account",
			"schema":      scimUserSchema,
			"meta":        map[string]any{"resourceType": "ResourceType"},
		},
		map[string]any{
			"schemas":     []string{scimResourceTypeSchema},
			"id":          "Group",
			"name":        "Group",
			"endpoint":    "/Groups",
			"description": "Group",
			"schema":      scimGroupSchema,
			"meta":        map[string]any{"resourceType": "ResourceType"},
		},
	}
	h.writeSCIM(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: int64(len(resources)),
		StartIndex:   1,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}

// Schemas handles GET /scim/v2/Schemas (RFC 7643 §7): the core User and
// Group schema definitions, limited to the attributes this slice maps.
func (h *SCIMHandler) Schemas(w http.ResponseWriter, r *http.Request) {
	attr := func(name, typ string, multi bool) map[string]any {
		return map[string]any{
			"name":        name,
			"type":        typ,
			"multiValued": multi,
			"required":    false,
			"mutability":  "readWrite",
			"returned":    "default",
			"uniqueness":  "none",
		}
	}
	resources := []any{
		map[string]any{
			"id":          scimUserSchema,
			"name":        "User",
			"description": "User Account",
			"attributes": []any{
				attr("userName", "string", false),
				attr("name", "complex", false),
				attr("emails", "complex", true),
				attr("active", "boolean", false),
			},
			"meta": map[string]any{"resourceType": "Schema"},
		},
		map[string]any{
			"id":          scimGroupSchema,
			"name":        "Group",
			"description": "Group",
			"attributes": []any{
				attr("displayName", "string", false),
				attr("members", "complex", true),
			},
			"meta": map[string]any{"resourceType": "Schema"},
		},
	}
	h.writeSCIM(w, http.StatusOK, scimListResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: int64(len(resources)),
		StartIndex:   1,
		ItemsPerPage: len(resources),
		Resources:    resources,
	})
}

// --- helpers ---

// scimPaging parses SCIM's 1-based startIndex + count params, clamping
// count to [1, scimMaxListResult] and the legacy offset to the same bounded
// ceiling used by the rest of the API.
func scimPaging(r *http.Request) (startIndex, count int) {
	startIndex = 1
	if s := r.URL.Query().Get("startIndex"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 1 {
			startIndex = v
		}
	}
	if startIndex-1 > int(maxPaginationOffset) {
		startIndex = int(maxPaginationOffset) + 1
	}
	count = 20
	if s := r.URL.Query().Get("count"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			count = v
		}
	}
	if count < 1 {
		count = 20
	}
	if count > scimMaxListResult {
		count = scimMaxListResult
	}
	return startIndex, count
}

// parseUserNameEqFilter extracts X from `userName eq "X"`. Returns ""
// for any other (unsupported) filter — the caller then falls through to
// an unfiltered list, which is a safe SCIM degradation.
func parseUserNameEqFilter(filter string) string {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return ""
	}
	lower := strings.ToLower(filter)
	if !strings.HasPrefix(lower, "username eq ") {
		return ""
	}
	val := strings.TrimSpace(filter[len("userName eq "):])
	return strings.Trim(val, `"`)
}

func (h *SCIMHandler) writeSCIM(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *SCIMHandler) scimError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"schemas": []string{scimErrorSchema},
		"detail":  detail,
		"status":  strconv.Itoa(status),
	})
}
