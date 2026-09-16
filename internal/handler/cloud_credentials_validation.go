package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- Utility helpers used by tests + caller wiring --------------------

// decodeStoredTargetRefs parses the JSONB column into a stable Go slice.
// Malformed values default to "no refs" — the row is still usable for
// list/get, just doesn't enqueue any materializations until the
// operator fixes it via PUT.
func decodeStoredTargetRefs(raw json.RawMessage) []TargetRef {
	if len(raw) == 0 {
		return []TargetRef{}
	}
	var out []TargetRef
	if err := json.Unmarshal(raw, &out); err != nil {
		return []TargetRef{}
	}
	if out == nil {
		out = []TargetRef{}
	}
	return out
}

// diffTargetRefs returns refs present in `before` but not in `after`
// (cluster+namespace identity). The handler uses this on PUT to enqueue
// Secret deletion for dropped targets.
func diffTargetRefs(before, after []TargetRef) []TargetRef {
	keep := map[string]TargetRef{}
	for _, r := range after {
		keep[r.ClusterID.String()+"|"+r.Namespace] = r
	}
	out := make([]TargetRef, 0)
	for _, r := range before {
		key := r.ClusterID.String() + "|" + r.Namespace
		if _, present := keep[key]; !present {
			out = append(out, r)
		}
	}
	return out
}

// validatePatchData is the PUT-only relaxed validator. It enforces:
//   - Every value must be a string.
//   - For non-Generic providers, unknown keys are rejected.
//   - Empty string is accepted ONLY if the key is the SecretSentinel —
//     we let the merged-blob validator catch "blanked out a required
//     field" with the same error message the create path uses.
func validatePatchData(provider string, blob map[string]any) error {
	spec, ok := cloudcreds.LookupProvider(provider)
	if !ok {
		return fmt.Errorf("unknown provider %q", provider)
	}
	allowed := map[string]struct{}{}
	for _, k := range spec.RequiredKeys {
		allowed[k] = struct{}{}
	}
	for _, k := range spec.OptionalKeys {
		allowed[k] = struct{}{}
	}
	for key, raw := range blob {
		if !spec.AllowUnknownKeys {
			if _, ok := allowed[key]; !ok {
				return fmt.Errorf("unknown key %q for provider %q", key, spec.Name)
			}
		}
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("key %q must be a string, got %T", key, raw)
		}
	}
	return nil
}

// userIDFromRequest extracts the authenticated user's UUID for the
// created_by stamp. Returns a NULL pgtype.UUID when the request isn't
// JWT-authenticated (admin scripts, internal jobs) — the column is
// nullable on purpose.
func userIDFromRequest(r *http.Request) pgtype.UUID {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(user.ID)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
