package handler

import (
	"context"
	"crypto/ed25519"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

type ExtensionQuerier interface {
	ListUIExtensions(ctx context.Context) ([]sqlc.UIExtension, error)
	UpsertUIExtension(ctx context.Context, arg sqlc.UpsertUIExtensionParams) (sqlc.UIExtension, error)
	SetUIExtensionEnabled(ctx context.Context, arg sqlc.SetUIExtensionEnabledParams) (sqlc.UIExtension, error)
	SetUIExtensionBundleVerified(ctx context.Context, arg sqlc.SetUIExtensionBundleVerifiedParams) (sqlc.UIExtension, error)
}

// ExtensionMutationTx is the transaction-bound extension state + mandatory
// audit surface. Extension mutations do not enqueue reconciliation work: no
// extension worker owns such a task, and inventing an outbox task would create
// durable work that can never execute.
type ExtensionMutationTx interface {
	ExtensionQuerier
	GetUIExtensionByNameForUpdate(ctx context.Context, name string) (sqlc.UIExtension, error)
	audit.OutboxQuerier
}

type extensionRunTxFunc func(context.Context, func(ExtensionMutationTx) error) error

type ExtensionHandler struct {
	queries ExtensionQuerier
	auditor any
	current string
	runTx   extensionRunTxFunc
	// trustedKey is the Ed25519 public key extension bundles must be
	// signed with. Nil means no key is configured: bundle verification
	// fails closed (executable bundles are gated) until an operator
	// supplies a trusted key via SetTrustedBundleKey.
	trustedKey ed25519.PublicKey
	// engine + bindings power §DataProxy's per-call RBAC re-check against
	// the REQUESTING user's own bindings (never the extension's). Wired via
	// SetRBAC; when nil the data proxy fails closed (503).
	engine   *rbac.Engine
	bindings ExtensionBindingsQuerier
	// upstream dispatches a resolved DataSourceRef to the in-process handler
	// the UI already uses (the second RBAC gate). Wired via SetUpstream; nil
	// => the proxy reports the upstream is not configured (502) AFTER both
	// the RBAC and allowlist gates have already passed, so a denied caller
	// can never reach it.
	upstream ExtensionUpstream
	// tickets validates Tier-2 X-Extension-Ticket headers (§BridgeProtocol).
	// Wired via SetExtensionTickets by the bridge phase; nil => only the
	// browser-session (Tier-1) auth path is available.
	tickets ExtensionTicketValidator
	// issuer mints the short-lived, scoped bridge ticket the iframe asks for
	// via ext/token.request. Wired via SetExtensionTickets alongside the
	// validator; nil => the ticket-issuance endpoint fails closed (503).
	issuer ExtensionTicketIssuer
}

func NewExtensionHandler(queries ExtensionQuerier) *ExtensionHandler {
	return &ExtensionHandler{queries: queries, auditor: queries, current: version.Version}
}

// SetRunTx wires production extension mutations to a database transaction.
func (h *ExtensionHandler) SetRunTx(runTx extensionRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

// TransactionalAuditWired is a production wiring probe.
func (h *ExtensionHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

// SetAuditWriter wires audit records for extension data-proxy reads and bridge
// ticket issuance. Registry mutations use only the transaction-bound outbox.
func (h *ExtensionHandler) SetAuditWriter(a any) {
	if h != nil {
		h.auditor = a
	}
}

func (h *ExtensionHandler) SetCurrentVersion(v string) {
	if h == nil {
		return
	}
	h.current = strings.TrimSpace(v)
}
