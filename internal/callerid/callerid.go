// Package callerid mints and resolves the caller identity carried on tunnel
// payloads (protocol.CallerIdentity).
//
// It exists so there is exactly ONE spelling of the canonical astronomer
// subject and exactly ONE machine-origin predicate. Before this package the
// subject string was built in handler.CallerScope.ImpersonationHeaders (dead
// code, referenced only by its own test) and nowhere else; two spellings of an
// identity is how they drift apart later.
//
// Contract (docs/design/downstream-impersonation.md §7):
//
//   - Identity is a typed payload field, never a forwarded header. Nothing here
//     reads a request header, query param, or body field.
//   - It is populated from the authenticated session only.
//   - Machine origin is an EXPLICIT POSITIVE marker taken from the exemption
//     list in §10, held here as DATA (see exemptions), not as scattered `if`s.
//   - The absence of a user is NOT machine. Resolve returns an unattributed
//     identity in that case, and protocol.CallerIdentity.IsMachine() is false
//     for it.
//
// PHASE 0: everything below populates. Nothing consumes.
package callerid

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Source names one entry of the machine-origin exemption list in the design
// doc §10 — "machine identities that must never be impersonated".
type Source string

const (
	// SourceArgoCDProxy is the bundled-ArgoCD internal k8s proxy, on both the
	// dedicated internal listener and the compatibility route on the public
	// listener. Token-gated, no human in the request. §10.1.
	SourceArgoCDProxy Source = "argocd-proxy"
	// SourceSelfManage is astronomer managing its own install into
	// AstronomerOwnedNamespaces (agent reconcile, agent RBAC, project
	// self-management). §10.2.
	SourceSelfManage Source = "self-manage"
	// SourceCrossPodTransport is the PSK-gated cross-pod internal fallback.
	// TRANSPORT, NOT ORIGIN: a user-originated request that lands on a
	// non-owner replica arrives here still user-originated, so this source is
	// only applied when no identity is already present. See Transport below.
	// §10.3.
	SourceCrossPodTransport Source = "cross-pod-transport"
	// SourceKubectlShell is kubectl-shell session provisioning and teardown
	// (ServiceAccount / Role / Binding / Pod), plus the relay into the
	// resulting shell pod. The shell's per-caller enforcement is already
	// delivered by CallerScope and impersonation must not be layered on it.
	// §10.4.
	SourceKubectlShell Source = "kubectl-shell"
	// SourceAgentLifecycle is self-upgrade, decommission, heartbeat/health,
	// informers and the SSAR self-probes. §10.5.
	SourceAgentLifecycle Source = "agent-lifecycle"
	// SourceBaselineReconcile is the baseline ApplicationSet / CRD reconcile
	// loops. §10.6.
	SourceBaselineReconcile Source = "baseline-reconcile"
)

// Exemption is one row of the §10 list. Keeping the list as data — rather than
// as an `if` in each of four files — is design item 10: adding an exemption is
// a table entry plus a test, and dropping one is visible in a diff.
type Exemption struct {
	Source Source
	// Reason is the design-doc justification, carried into logs/tests so the
	// exemption is self-describing at the point of use.
	Reason string
	// Transport marks an entry that is a HOP, not an origin. For those the
	// machine marker must never overwrite an identity the request already
	// carries — it only fills one in when the hop genuinely started here.
	Transport bool
}

// exemptions is THE list. Nothing outside this table may be machine-origin.
var exemptions = map[Source]Exemption{
	SourceArgoCDProxy: {
		Source: SourceArgoCDProxy,
		Reason: "bundled ArgoCD cluster-cache/apply traffic; token-gated, no human in the request, and an impersonated identity would fail the whole cache sync",
	},
	SourceSelfManage: {
		Source: SourceSelfManage,
		Reason: "astronomer managing its own install inside AstronomerOwnedNamespaces",
	},
	SourceCrossPodTransport: {
		Source:    SourceCrossPodTransport,
		Reason:    "PSK-gated sibling-replica hop; transport, not origin",
		Transport: true,
	},
	SourceKubectlShell: {
		Source: SourceKubectlShell,
		Reason: "shell session provisioning/teardown and relay; enforcement is the session ServiceAccount, not impersonation",
	},
	SourceAgentLifecycle: {
		Source: SourceAgentLifecycle,
		Reason: "agent self-upgrade, decommission, health, informers, SSAR self-probes",
	},
	SourceBaselineReconcile: {
		Source: SourceBaselineReconcile,
		Reason: "baseline ApplicationSet and CRD reconcile loops",
	},
}

// Exempt looks a source up in the §10 table.
func Exempt(s Source) (Exemption, bool) {
	e, ok := exemptions[s]
	return e, ok
}

// Exemptions returns the whole table, for tests and for operator-facing docs.
func Exemptions() []Exemption {
	out := make([]Exemption, 0, len(exemptions))
	for _, e := range exemptions {
		out = append(out, e)
	}
	return out
}

// UserSubject mints the canonical astronomer user subject. THE minter — the
// promoted, deduplicated form of the old
// CallerScope.ImpersonationHeaders()["Impersonate-User"] value.
func UserSubject(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return protocol.UserSubjectPrefix + id.String()
}

// RoleGroup mints the canonical astronomer role-group subject. Reserved for
// Phase 1: nothing populates CallerIdentity.Groups in Phase 0.
func RoleGroup(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return protocol.RoleGroupPrefix + slug
}

// MachineSubject mints the subject for a machine origin. The source is part of
// the subject, so a machine identity says WHICH exemption applies rather than
// being an anonymous "not a user".
func MachineSubject(s Source) string {
	return protocol.MachineSubjectPrefix + string(s)
}

// Machine builds the identity for a §10 exemption. An unknown source yields an
// unattributed identity rather than a machine one — the table is the authority,
// so a typo cannot mint a machine marker.
func Machine(s Source) protocol.CallerIdentity {
	if _, ok := exemptions[s]; !ok {
		return protocol.CallerIdentity{}
	}
	return protocol.CallerIdentity{
		User:   MachineSubject(s),
		Origin: protocol.OriginMachine,
	}
}

type contextKey string

const (
	machineSourceKey contextKey = "callerid.machine-source"
	userSubjectKey   contextKey = "callerid.user"
)

// WithMachine stamps a machine origin onto ctx. Call it at the site that KNOWS
// the request has no human behind it — the ArgoCD proxy gate, a reconciler
// loop, shell provisioning. Never call it as a fallback for "couldn't find a
// user".
func WithMachine(ctx context.Context, s Source) context.Context {
	if _, ok := exemptions[s]; !ok {
		return ctx
	}
	return context.WithValue(ctx, machineSourceKey, s)
}

// MachineSourceFromContext returns the machine marker stamped on ctx, if any.
func MachineSourceFromContext(ctx context.Context) (Source, bool) {
	if ctx == nil {
		return "", false
	}
	s, ok := ctx.Value(machineSourceKey).(Source)
	return s, ok
}

// IsMachineOrigin is THE machine-origin predicate for a server-side context.
// One implementation, one call shape, consulted everywhere the question is
// asked.
func IsMachineOrigin(ctx context.Context) bool {
	_, ok := MachineSourceFromContext(ctx)
	return ok
}

// WithUser stamps an already-authenticated user onto ctx for code paths that
// authenticate outside the standard middleware chain and therefore have no
// middleware.AuthenticatedUser in context — specifically the exec and logs
// WebSocket front doors, which validate a single-use stream ticket or a bearer
// token themselves and hold the resulting uuid.
//
// The input MUST be the output of an authentication check. This is the same
// "server-side, from the session" source as GetAuthenticatedUser; it is not a
// second, weaker channel, and it reads nothing from the request.
func WithUser(ctx context.Context, id uuid.UUID) context.Context {
	if id == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, userSubjectKey, UserSubject(id))
}

// Resolve derives the identity to stamp on a payload built while serving ctx.
//
// Precedence, and the reason for it:
//
//  1. An explicit machine marker wins. A reconciler that also happens to run
//     under a user's request context (self-management triggered from a UI
//     click) is still a machine path — §10 says those must never be
//     impersonated, so the positive marker is authoritative.
//  2. An explicitly stamped authenticated user (exec/logs front doors).
//  3. middleware.GetAuthenticatedUser — the normal authenticated route.
//  4. Otherwise UNATTRIBUTED. Not machine. Deliberately: §7 invariant 4.
//
// RequestID is taken from middleware.GetGeneratedRequestID, NOT
// GetCorrelationID: the correlation id may have been supplied by the client in
// X-Correlation-Id / X-Request-ID, and §7 invariant 3 says every field of this
// envelope comes from the session. A client-chosen id would let the caller
// forge the correlation between a downstream action and an arbitrary
// control-plane request; an empty id is strictly better than a forged one, so
// the untrusted value is dropped and the field is simply absent on requests
// where the caller brought its own correlation id.
func Resolve(ctx context.Context) protocol.CallerIdentity {
	if ctx == nil {
		return protocol.CallerIdentity{}
	}
	requestID := middleware.GetGeneratedRequestID(ctx)
	if s, ok := MachineSourceFromContext(ctx); ok {
		id := Machine(s)
		id.RequestID = requestID
		return id
	}
	if subject, ok := ctx.Value(userSubjectKey).(string); ok && subject != "" {
		return protocol.CallerIdentity{User: subject, Origin: protocol.OriginUser, RequestID: requestID}
	}
	if user, ok := middleware.GetAuthenticatedUser(ctx); ok && user != nil {
		if id, err := uuid.Parse(user.ID); err == nil && id != uuid.Nil {
			return protocol.CallerIdentity{User: UserSubject(id), Origin: protocol.OriginUser, RequestID: requestID}
		}
	}
	return protocol.CallerIdentity{RequestID: requestID}
}

// Fill returns existing when it is already attributed, and the ctx-resolved
// identity otherwise. This is the rule that makes the cross-pod hop correct:
// the sibling forwards what it was given verbatim (design §10.3) and only
// derives an identity when the hop genuinely originated locally.
func Fill(ctx context.Context, existing protocol.CallerIdentity) protocol.CallerIdentity {
	if existing.Attributed() {
		return existing
	}
	return Resolve(ctx)
}
