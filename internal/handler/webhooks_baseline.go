package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/compliance"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
)

func (h *WebhookHandler) requireSuperuser(r *http.Request) error {
	return requireSuperuserFromContext(r, h.queries)
}

// activeBaselineQuerier is the narrow surface shared by the
// webhook + SMTP handlers' baseline-guard check (T6.064). Defined
// here next to the only caller so a future refactor can move it to
// a dedicated package without disturbing handler imports.
type activeBaselineQuerier interface {
	GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error)
	GetComplianceBaseline(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error)
}

// BaselineOverrideChecker reports whether the caller of r holds the
// RBAC override permission that lets them bypass the compliance
// deletion guard (deleting a webhook / disabling SMTP that the active
// baseline marks required). Production wires this from the RBAC
// engine via NewBaselineOverrideChecker; when a handler's checker is
// nil the guard is never bypassed (fail-closed — only an explicit
// override unblocks a required-config mutation).
type BaselineOverrideChecker func(r *http.Request) bool

// baselineOverrideAllowed is the shared "does the caller hold the
// override?" predicate. nil checker → false (guard stays in force).
func baselineOverrideAllowed(check BaselineOverrideChecker, r *http.Request) bool {
	return check != nil && check(r)
}

// BaselineOverrideEngine is the narrow RBAC surface
// NewBaselineOverrideChecker needs: resolve a user's bindings and ask
// the engine whether they satisfy a (resource, verb). *rbac.Engine +
// the cached RBAC querier satisfy this in production.
type BaselineOverrideEngine interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// baselineOverrideResource/Verb is the global permission a caller must
// hold to bypass the compliance deletion guard. settings:manage is the
// platform-settings/compliance management grant — the built-in
// owner/admin templates carry it, and an operator can mint a narrow
// role granting exactly it for a break-glass override.
const (
	baselineOverrideResource = rbac.ResourceSettings
	baselineOverrideVerb     = rbac.VerbManage
)

// NewBaselineOverrideChecker builds the production override predicate.
// A request passes when its authenticated user holds an EXPLICIT
// settings:manage grant at global scope. We use CheckExplicitPermission
// (not CheckPermission) on purpose: the only role that reaches the
// guarded delete handlers is superuser, and the implicit superuser
// bypass in CheckPermission would make every superuser pass the override
// — turning the 409 guard into dead code. Break-glass requires a real
// granted permission, so a superuser is held by the guard unless an
// operator mints a role carrying settings:manage and binds it to them.
// nil engine → nil checker (guard never bypassed).
func NewBaselineOverrideChecker(engine *rbac.Engine, q BaselineOverrideEngine) BaselineOverrideChecker {
	if engine == nil || q == nil {
		return nil
	}
	return func(r *http.Request) bool {
		user, ok := reqctx.AuthenticatedUser(r.Context())
		if !ok || user == nil {
			return false
		}
		bindings, err := q.GetUserBindings(r.Context(), user.ID)
		if err != nil {
			return false
		}
		return engine.CheckExplicitPermission(bindings, baselineOverrideResource, baselineOverrideVerb, uuid.Nil, uuid.Nil)
	}
}

// activeBaselineRequiresWebhook returns (slug, true) when the active
// baseline lists the given webhook subscription name in its
// required_webhooks set. (slug, false) when the name is not
// required, or when there is no active baseline.
//
// Failures (DB error, unknown slug) degrade open — we return
// (..., false) rather than block deletes when the lookup itself is
// broken. Logging happens in the caller's audit row when applicable.
func activeBaselineRequiresWebhook(ctx context.Context, q activeBaselineQuerier, name string) (string, bool) {
	slug, spec, ok := loadActiveBaselineSpec(ctx, q)
	if !ok {
		return "", false
	}
	for _, w := range spec.RequiredWebhooks {
		if w == name {
			return slug, true
		}
	}
	return "", false
}

// loadActiveBaselineSpec resolves the active baseline application to
// its slug + populated spec. Returns (..., false) on any error so the
// caller can decide whether to fail open or closed.
func loadActiveBaselineSpec(ctx context.Context, q activeBaselineQuerier) (string, compliance.BaselineSpec, bool) {
	if q == nil {
		return "", compliance.BaselineSpec{}, false
	}
	app, err := q.GetActiveComplianceBaselineApplication(ctx)
	if err != nil {
		return "", compliance.BaselineSpec{}, false
	}
	base, err := q.GetComplianceBaseline(ctx, app.BaselineID)
	if err != nil {
		return "", compliance.BaselineSpec{}, false
	}
	b, ok := compliance.BySlug(base.Slug)
	if !ok {
		return "", compliance.BaselineSpec{}, false
	}
	return base.Slug, b.Spec, true
}
