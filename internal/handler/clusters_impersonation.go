// Package handler — downstream-impersonation mode flag (Phase 0 plumbing).
//
// The tri-state per-cluster flag from docs/design/downstream-impersonation.md
// §5, stored in `clusters.annotations`. This file is the whole of its write
// gate and its read surface.
//
// PHASE 0 STATUS: the flag is stored, validated, superuser-gated, capability-
// gated and surfaced on the cluster response, and NOTHING READS IT to change
// behaviour. `off` is the default and the only value any code path acts on
// (by doing nothing). See callerid.Modes.
package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// clusterDownstreamImpersonationMode reads the effective mode off a cluster's
// annotations. Absent / blank / unrecognized all read as "off".
//
// Reads the raw JSONB rather than clusterAnnotations' map[string]string so a
// blob carrying any non-string sibling (legal in this free-form column) still
// reports the mode instead of silently reading as "off".
func clusterDownstreamImpersonationMode(raw json.RawMessage) string {
	return string(callerid.ModeFromJSON(raw))
}

// clusterImpersonationCapabilityQuerier is the optional slice of the querier
// needed to check whether the agent has advertised the impersonation
// capability. Asserted rather than required (same pattern as
// clusterOwnershipQuerier) so test fakes and pre-migration wiring keep working
// — and a querier that does not implement it fails CLOSED: no advertisement
// means `enforce` is refused.
type clusterImpersonationCapabilityQuerier interface {
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
}

// agentAdvertisedImpersonation reports whether the cluster's most recent
// heartbeat advertised protocol.FeatureImpersonation as ENABLED.
//
// This is the server half of the capability handshake (design §8 item 6). The
// agent probes its own rights with a SelfSubjectAccessReview and reports the
// result; without it, flipping a cluster to `enforce` against an agent that
// lacks the grant would 403 every request through that cluster — the exact
// failure the plan's acceptance criteria call out.
func agentAdvertisedImpersonation(ctx context.Context, q any, clusterID uuid.UUID) bool {
	capQ, ok := q.(clusterImpersonationCapabilityQuerier)
	if !ok {
		return false
	}
	row, err := capQ.GetClusterHealthStatus(ctx, clusterID)
	if err != nil || len(row.Conditions) == 0 {
		return false
	}
	var conditions struct {
		EnabledFeatures []string `json:"enabled_features"`
	}
	if err := json.Unmarshal(row.Conditions, &conditions); err != nil {
		return false
	}
	for _, f := range conditions.EnabledFeatures {
		if f == protocol.FeatureImpersonation {
			return true
		}
	}
	return false
}

// guardDownstreamImpersonationAnnotation is the write gate for the mode flag on
// cluster create/update. It returns the annotations to persist and false when
// it has already written an error response.
//
// Rules, in order:
//
//  1. An explicitly supplied value that is not one of off|attribute|enforce is
//     a 400. It is NOT coerced to "off" — silently coercing a typo is a footgun
//     in the permissive direction once Phase 2 ships enforcement.
//  2. No change (same effective mode, or key absent) ⇒ preserve. A caller that
//     PUTs an annotation blob without the key does not clear the flag, and does
//     not need to be a superuser to edit the cluster's other fields.
//  3. Any change ⇒ SUPERUSER ONLY. A non-superuser explicitly supplying a
//     different value gets a 403; they cannot raise it and cannot lower it.
//  4. Raising to `enforce` additionally requires the agent to have advertised
//     the capability. Fails closed.
//
// The caller's blob is treated as map[string]json.RawMessage throughout, NOT as
// map[string]string. `annotations` is free-form JSONB and a caller may legally
// send `{"replicas":3}`; flattening to a string map would drop every non-string
// sibling on the floor when this function has to splice the mode key back in.
func guardDownstreamImpersonationAnnotation(
	w http.ResponseWriter,
	r *http.Request,
	q any,
	clusterID uuid.UUID,
	stored json.RawMessage,
	incoming json.RawMessage,
) (json.RawMessage, bool) {
	storedMode := callerid.ModeFromJSON(stored)
	incomingObj, isObject := annotationObject(incoming)

	rawMessage, present := incomingObj[callerid.ModeAnnotation]
	if !present {
		// Key omitted: preserve whatever is stored rather than clearing it.
		if storedMode == callerid.ModeOff {
			// Nothing to preserve. Hand the caller's bytes back UNTOUCHED —
			// this is the default path for every cluster, and it must stay
			// byte-identical to not having this gate at all.
			return incoming, true
		}
		if !isObject {
			// A non-object blob (`null`, an array, a scalar) cannot have a key
			// spliced into it, and letting it through would silently CLEAR a
			// superuser-set mode. Fail closed instead. Unreachable while the
			// flag is off everywhere, which is why it costs nothing.
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
				"annotations must be a JSON object while "+callerid.ModeAnnotation+" is set")
			return nil, false
		}
		return spliceAnnotation(incomingObj, callerid.ModeAnnotation, string(storedMode), incoming), true
	}

	var rawValue string
	if err := json.Unmarshal(rawMessage, &rawValue); err != nil {
		// The key is present but is not a JSON string. Rejecting is the same
		// fail-closed posture as an unrecognized string value below; coercing
		// or ignoring it would let a caller smuggle the key past the gate.
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
			"Invalid "+callerid.ModeAnnotation+" value; expected off, attribute, or enforce")
		return nil, false
	}
	mode, ok := callerid.ParseMode(rawValue)
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
			"Invalid "+callerid.ModeAnnotation+" value; expected off, attribute, or enforce")
		return nil, false
	}
	if mode == storedMode {
		return incoming, true
	}

	// A real change. Superuser only.
	if _, ok := RequireSuperuser(w, r, clusterUserByIDQuerier(q), SuperuserGateConfig{
		ForbiddenMessage: "Only a superuser may change the downstream-impersonation mode",
	}); !ok {
		return nil, false
	}
	if mode.RequiresAgentCapability() && !agentAdvertisedImpersonation(r.Context(), q, clusterID) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"The cluster agent has not advertised the impersonation capability; it cannot be moved to enforce")
		return nil, false
	}
	return incoming, true
}

// annotationObject decodes a raw `clusters.annotations` blob into its top-level
// keys WITHOUT flattening the values. ok is false when the blob is absent or is
// not a JSON object (`null`, an array, a scalar, malformed bytes), in which case
// the returned map is nil and reads of it are still safe.
//
// This is deliberately not clusterAnnotations: that helper decodes into
// map[string]string and returns an EMPTY map both when the blob is empty and
// when any value is a non-string, which makes "no annotations" and "annotations
// this helper cannot represent" indistinguishable. Every caller here needs to
// tell those apart.
func annotationObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		// obj == nil covers the JSON literal `null`, which unmarshals into a
		// map without error and leaves it nil — the nil-map assignment panic
		// this helper exists to make impossible.
		return nil, false
	}
	return obj, true
}

// spliceAnnotation sets one string-valued key on an already-decoded annotation
// object and re-encodes it, leaving every sibling value byte-identical to what
// the caller sent (json.RawMessage round-trips verbatim). Falls back to the
// caller's original bytes if the re-encode somehow fails, since a silent 500 on
// a cluster rename would be worse than a no-op on a key nothing reads yet.
func spliceAnnotation(obj map[string]json.RawMessage, key, value string, fallback json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	obj[key] = encoded
	raw, err := json.Marshal(obj)
	if err != nil {
		return fallback
	}
	return raw
}

// clusterUserByIDQuerier adapts the cluster querier to the superuser gate's
// interface. Returns nil when the querier cannot resolve users, which
// RequireSuperuser turns into a 503 rather than an accidental allow.
func clusterUserByIDQuerier(q any) UserByIDQuerier {
	if uq, ok := q.(UserByIDQuerier); ok {
		return uq
	}
	return nil
}
