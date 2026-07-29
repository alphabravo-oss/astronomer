package protocol

import "strings"

// CallerOrigin discriminates, positively, on whose behalf a tunnel payload was
// built. It is the discriminator required by
// docs/design/downstream-impersonation.md §7 invariant 4:
//
//	"Machine origin is an explicit positive marker, not the absence of a user.
//	 Inferring 'no user ⇒ machine' fails open the moment an authenticated route
//	 forgets to populate the field."
//
// The zero value is therefore deliberately NEITHER user nor machine. A payload
// built by a site that has not been taught to stamp an origin — or by an older
// server — arrives as OriginUnset, which every predicate below treats as
// "unattributed": IsUser() is false, IsMachine() is false. There is no
// combination of empty fields that yields machine semantics by accident.
type CallerOrigin string

const (
	// OriginUnset is the zero value: unattributed. Not user, not machine.
	// Any consumer that needs one of the two must fail closed on it rather
	// than pick a default.
	OriginUnset CallerOrigin = ""
	// OriginUser marks a payload built for a specific authenticated human
	// (or a human's API token). CallerIdentity.User is populated.
	OriginUser CallerOrigin = "user"
	// OriginMachine marks a payload built by astronomer itself with no human
	// in the request — the exemption list in the design doc §10. Stamped
	// positively at the site that knows it is a machine path; never inferred.
	OriginMachine CallerOrigin = "machine"
)

// Valid reports whether the origin is one of the two real values. OriginUnset
// is not valid — it means nobody stamped one.
func (o CallerOrigin) Valid() bool {
	return o == OriginUser || o == OriginMachine
}

// CallerIdentity is the typed identity envelope carried on a tunnel payload.
//
// Two hard rules, both from the design doc §7:
//
//  1. It is a TYPED FIELD, never a forwarded header. pkg/proxyhdr's allowlist
//     remains a hard deny for Impersonate-* / X-Remote-* precisely so a client
//     can never assert its own identity; this struct is populated server-side
//     from the authenticated session and nowhere else.
//  2. Origin is an explicit positive marker (see CallerOrigin).
//
// PHASE 0: these fields are POPULATED AND UNUSED. Nothing reads them to make
// an authorization or request-shaping decision, and the agent sets no
// Impersonate-* header. Consuming them is Phase 1 and is gated on the
// per-cluster downstream-impersonation mode.
type CallerIdentity struct {
	// User is the canonical astronomer subject. For OriginUser it is
	// "astronomer:user:<uuid>"; for OriginMachine it is
	// "astronomer:machine:<source>" naming the design-doc §10 exemption that
	// applies. Minted in exactly one place — internal/callerid.
	User string `json:"caller_user,omitempty"`
	// Groups is the astronomer role-group vocabulary
	// ("astronomer:role:<slug>"). Reserved: nothing populates it in Phase 0,
	// because deriving it needs a per-cluster binding resolution that belongs
	// with the enforcing path, not with session population.
	Groups []string `json:"caller_groups,omitempty"`
	// RequestID correlates this payload with the control-plane audit row for
	// the same request, and is EMPTY whenever that correlation cannot be
	// trusted.
	//
	// The control plane accepts a caller-supplied X-Correlation-Id /
	// X-Request-ID and echoes it into its own logs and audit rows. That value
	// is deliberately NOT carried here: it is the one identity-adjacent field a
	// client can choose, and a forged correlation is worse than no correlation
	// (§7 invariant 3 — the envelope is populated from the session only).
	// internal/callerid therefore fills this from
	// middleware.GetGeneratedRequestID, which yields a value only when the
	// server minted the id itself. Consumers must treat "" as "no correlation
	// available", never as an error.
	RequestID string `json:"caller_request_id,omitempty"`
	// Origin is the positive user/machine discriminator. See CallerOrigin.
	Origin CallerOrigin `json:"caller_origin,omitempty"`
}

// IsUser reports whether this identity positively names a human caller. Both
// halves are required: an OriginUser with no subject is malformed, not a user.
func (c CallerIdentity) IsUser() bool {
	return c.Origin == OriginUser && c.User != ""
}

// IsMachine reports whether this identity positively names an astronomer
// machine path. This is THE machine-origin predicate on the wire type; it
// consults nothing but the explicit marker.
func (c CallerIdentity) IsMachine() bool {
	return c.Origin == OriginMachine
}

// Attributed reports whether an origin was stamped at all. False means the
// building site is older than the identity plumbing (or was missed).
func (c CallerIdentity) Attributed() bool {
	return c.Origin.Valid()
}

// UserSubjectPrefix / MachineSubjectPrefix / RoleGroupPrefix are the canonical
// astronomer identity vocabulary. They live here, on the wire package, so the
// server, the agent, and any test share one spelling; internal/callerid is the
// only thing that concatenates them.
const (
	UserSubjectPrefix    = "astronomer:user:"
	MachineSubjectPrefix = "astronomer:machine:"
	RoleGroupPrefix      = "astronomer:role:"
)

// IsUserSubject / IsMachineSubject let a consumer classify a subject string
// without re-deriving the prefixes.
func IsUserSubject(s string) bool    { return strings.HasPrefix(s, UserSubjectPrefix) }
func IsMachineSubject(s string) bool { return strings.HasPrefix(s, MachineSubjectPrefix) }

// FeatureImpersonation is the capability name an agent advertises in
// HeartbeatPayload.EnabledFeatures / DeniedFeatures after probing its own
// rights with a SelfSubjectAccessReview. The server refuses to move a cluster
// to the `enforce` downstream-impersonation mode unless the agent has
// advertised it enabled — this is what prevents both the version-skew failure
// and the "flag on, every request 403s" failure.
const FeatureImpersonation = "impersonation"

// ImpersonationProbeSubject is the subject the agent's SSAR self-probe asks
// about. It is the pinned proxy identity from design Option D, and the probe is
// approach-independent in the safe direction: a NAMED check succeeds under an
// unbounded `impersonate users` grant too, so the probe never reports "denied"
// for an agent that actually holds the verb.
const ImpersonationProbeSubject = "astronomer:proxy"
