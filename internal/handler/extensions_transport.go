package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

// ExtensionBindingsQuerier resolves the caller's RBAC bindings. Same shape as
// rbac.BindingQuerier; declared locally so the handler package needn't import
// the middleware package.
type ExtensionBindingsQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// ExtensionTicketValidator consumes a single-use Tier-2 data ticket, returning
// the bound user id. It is the §BridgeProtocol ExtensionTicketStore; declared as
// an interface so the data proxy compiles before the bridge phase wires a store.
type ExtensionTicketValidator interface {
	Validate(token, extension, dataSourceID string, clusterID uuid.UUID) (uuid.UUID, error)
}

// ExtensionTicketIssuer mints a short-lived, narrowly-scoped Tier-2 bridge
// ticket (§BridgeProtocol). It backs ext/token.request: the bridge handler
// RBAC-checks the user for the dataSource first, then issues. The store hashes
// the opaque token at rest, makes it single-use, and TTL-bounds it (≤60s).
type ExtensionTicketIssuer interface {
	IssueToken(userID uuid.UUID, extension, dataSourceID string, clusterID uuid.UUID) (token string, expiresAt time.Time, err error)
}

// ExtensionUpstreamRequest is the server-built, validated upstream call. Every
// field is derived from the STORED manifest + validated context — no raw client
// field redirects it (no SSRF / traversal).
type ExtensionUpstreamRequest struct {
	Proxy     string
	Method    string
	Path      string            // placeholders already filled
	Query     map[string]string // declared keys only
	Body      json.RawMessage   // POST form submit only
	UserID    uuid.UUID
	ClusterID uuid.UUID
	ProjectID uuid.UUID
	Namespace string
}

// ExtensionUpstream dispatches a resolved upstream request in-process and
// returns the raw decoded payload (rows for a list, an object, or a series).
// The proxy projects/truncates the result; the upstream itself re-runs the
// host's own RBAC middleware as a second, independent gate.
type ExtensionUpstream func(ctx context.Context, req ExtensionUpstreamRequest) (any, error)

// SetRBAC wires the RBAC engine + bindings querier the data proxy uses to
// re-check the requesting user's own permissions on every call.
func (h *ExtensionHandler) SetRBAC(engine *rbac.Engine, bindings ExtensionBindingsQuerier) {
	if h == nil {
		return
	}
	h.engine = engine
	h.bindings = bindings
}

// SetUpstream wires the in-process upstream dispatcher (the second RBAC gate).
func (h *ExtensionHandler) SetUpstream(u ExtensionUpstream) {
	if h == nil {
		return
	}
	h.upstream = u
}

// SetExtensionTickets wires the Tier-2 data-ticket validator (§BridgeProtocol).
func (h *ExtensionHandler) SetExtensionTickets(v ExtensionTicketValidator) {
	if h == nil {
		return
	}
	h.tickets = v
}

// SetExtensionTicketIssuer wires the Tier-2 bridge ticket issuer backing
// ext/token.request (§BridgeProtocol). Typically the same store as the
// validator, set together at construction.
func (h *ExtensionHandler) SetExtensionTicketIssuer(i ExtensionTicketIssuer) {
	if h == nil {
		return
	}
	h.issuer = i
}
