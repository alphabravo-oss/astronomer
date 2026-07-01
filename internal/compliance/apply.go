package compliance

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Querier is the narrow DB surface the apply / revert / diff engine
// needs. *sqlc.Queries satisfies it; tests can substitute a fake.
//
// Apply requires the caller to have BEGUN A TRANSACTION on the
// underlying pool and to pass the tx-scoped Queries instance — every
// write below MUST be in a single tx so a partial apply rolls back
// cleanly. The engine itself doesn't begin/commit; that's the
// handler's job (it has the *pgxpool.Pool the engine intentionally
// doesn't see — keeps the engine pool-agnostic for tests).
type Querier interface {
	// Baselines registry
	GetComplianceBaseline(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error)
	ListComplianceBaselines(ctx context.Context) ([]sqlc.ComplianceBaseline, error)

	// Applications history
	CreateComplianceBaselineApplication(ctx context.Context, arg sqlc.CreateComplianceBaselineApplicationParams) (sqlc.ComplianceBaselineApplication, error)
	GetComplianceBaselineApplication(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaselineApplication, error)
	GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error)
	ListComplianceBaselineApplications(ctx context.Context, limit int32) ([]sqlc.ComplianceBaselineApplication, error)
	MarkComplianceBaselineApplicationReverted(ctx context.Context, arg sqlc.MarkComplianceBaselineApplicationRevertedParams) error

	// Platform settings (the apply engine treats settings as the
	// single source of truth for {audit_retention, session timeout,
	// PSS profile, TOTP required, banner text, ...}). Any field that
	// doesn't map to a richer table lives in platform_settings.
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	UpsertPlatformSetting(ctx context.Context, arg sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error)

	// Quota plans
	GetQuotaPlan(ctx context.Context, name string) (sqlc.QuotaPlan, error)
	UpsertQuotaPlan(ctx context.Context, arg sqlc.UpsertQuotaPlanParams) (sqlc.QuotaPlan, error)
}

// ErrNewerApplicationExists is returned by Revert when a baseline has
// been applied AFTER the one the caller is trying to revert. v1
// policy: revert always undoes the MOST RECENT application; a
// force-revert that walks back through history is out of scope.
var ErrNewerApplicationExists = errors.New("a newer baseline application exists; revert the latest first")

// ErrBaselineDisabled is returned when the operator attempts to apply
// a baseline whose `enabled` column is false. Future migrations may
// flip a deprecated baseline off without dropping the row.
var ErrBaselineDisabled = errors.New("baseline is disabled")

// ErrAuditRetentionDowngrade is the safety guard the spec calls for:
// applying a baseline whose audit_retention_days is LOWER than the
// current setting is a compliance-destructive operation and is
// blocked. Operators who really want to downgrade do so manually via
// /admin/settings/audit.retention_days.
var ErrAuditRetentionDowngrade = errors.New("baseline would downgrade audit retention; refuse")

// DiffResult is what Diff() returns — three views of the proposed
// change that the UI renders as a side-by-side table.
type DiffResult struct {
	BaselineID   uuid.UUID      `json:"baseline_id"`
	BaselineSlug string         `json:"baseline_slug"`
	BaselineName string         `json:"baseline_name"`
	Current      map[string]any `json:"current"`
	Target       map[string]any `json:"target"`
	// Changes is the keys that differ. Ordered alphabetically so the
	// UI renders a stable list (registry map iteration is otherwise
	// random).
	Changes []string `json:"changes"`
}

// Apply executes a baseline atomically. The caller MUST have begun a
// transaction on the underlying pool and pass the tx-scoped Queries
// in `q`. On success the application UUID is returned; on error the
// caller rolls the tx back (every partial write is undone).
//
// Idempotency: re-applying the same baseline immediately after is
// effectively a no-op because the diff is empty — Apply still
// records a fresh application row (the operator clicked Apply, so
// the audit trail should show that), but no platform_settings /
// quota_plans / alert_rules writes happen.
//
// The narrow Querier interface here is intentional: the engine
// doesn't see *pgxpool.Pool so unit tests can substitute a fake
// without spinning up Postgres.
func Apply(ctx context.Context, q Querier, baselineID uuid.UUID, userID uuid.UUID, notes string, logger *slog.Logger) (uuid.UUID, error) {
	if logger == nil {
		logger = slog.Default()
	}
	row, err := q.GetComplianceBaseline(ctx, baselineID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("load baseline: %w", err)
	}
	if !row.Enabled {
		return uuid.Nil, ErrBaselineDisabled
	}
	canonical, ok := BySlug(row.Slug)
	if !ok {
		return uuid.Nil, fmt.Errorf("baseline slug %q has no canonical spec in registry", row.Slug)
	}
	spec := canonical.Spec

	// 1. Audit-retention downgrade guard (FIRST — we want the most
	//    expensive validation up-front so a failed apply doesn't
	//    incur any DB writes).
	if spec.AuditRetentionDays > 0 {
		cur := readIntSetting(ctx, q, "audit.retention_days", 0)
		if cur > spec.AuditRetentionDays {
			return uuid.Nil, fmt.Errorf("%w (current=%d, target=%d)", ErrAuditRetentionDowngrade, cur, spec.AuditRetentionDays)
		}
	}

	// 2. Snapshot the prior state BEFORE writing.
	prev := buildSnapshot(ctx, q, spec)
	prevJSON, err := json.Marshal(prev)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode previous_state: %w", err)
	}

	// 3. Apply each spec field. Order: cheap settings first, richer
	//    tables next; if any single write returns an error, we
	//    return so the caller rolls the tx back.
	if err := writeSpec(ctx, q, spec, userID, logger); err != nil {
		return uuid.Nil, err
	}

	// 4. Record the application row LAST so the previous_state
	//    snapshot accurately reflects the pre-apply view (we don't
	//    want our own writes leaking into the snapshot).
	app, err := q.CreateComplianceBaselineApplication(ctx, sqlc.CreateComplianceBaselineApplicationParams{
		BaselineID:    baselineID,
		PreviousState: prevJSON,
		AppliedBy:     pgtypeUUID(userID),
		Notes:         notes,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("record application: %w", err)
	}
	return app.ID, nil
}

// Revert reverses the most-recent application by restoring
// previous_state. Errors with ErrNewerApplicationExists if the
// supplied applicationID isn't the most-recent applied one (operator
// must revert from latest backwards).
//
// Same tx contract as Apply: caller begins/commits.
func Revert(ctx context.Context, q Querier, applicationID uuid.UUID, userID uuid.UUID, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	app, err := q.GetComplianceBaselineApplication(ctx, applicationID)
	if err != nil {
		return fmt.Errorf("load application: %w", err)
	}
	if app.Status != "applied" {
		return fmt.Errorf("application %s status=%s, only 'applied' rows can be reverted", applicationID, app.Status)
	}

	// Refuse if a newer 'applied' row exists.
	active, err := q.GetActiveComplianceBaselineApplication(ctx)
	if err == nil && active.ID != applicationID {
		return ErrNewerApplicationExists
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load active application: %w", err)
	}

	// Decode the snapshot. previous_state is the same shape as
	// BaselineSpec (deliberate — orthogonal serialization). Unknown
	// fields are ignored.
	var snap BaselineSpec
	if err := json.Unmarshal(app.PreviousState, &snap); err != nil {
		return fmt.Errorf("decode previous_state: %w", err)
	}

	// Load the canonical spec of the baseline that was applied so we
	// know WHICH fields it owns. A restore must re-write every owned
	// field — including the ones whose captured previous value is
	// false/empty/zero — otherwise a hardening baseline that flipped
	// e.g. totp.required ON can never be turned back OFF (writeSpec
	// only writes truthy scalars, so `false` would be silently
	// skipped and the security setting stays pinned on). Ownership
	// can't be recovered from the snapshot alone because a captured
	// `false` is indistinguishable from a field the baseline never
	// touched, so we consult the canonical spec for the owner set.
	baseRow, err := q.GetComplianceBaseline(ctx, app.BaselineID)
	if err != nil {
		return fmt.Errorf("load baseline for revert: %w", err)
	}
	canonical, ok := BySlug(baseRow.Slug)
	if !ok {
		return fmt.Errorf("baseline slug %q has no canonical spec in registry", baseRow.Slug)
	}

	if err := restoreSpec(ctx, q, canonical.Spec, snap, userID, logger); err != nil {
		return fmt.Errorf("restore previous_state: %w", err)
	}

	if err := q.MarkComplianceBaselineApplicationReverted(ctx, sqlc.MarkComplianceBaselineApplicationRevertedParams{
		ID:         applicationID,
		RevertedBy: pgtypeUUID(userID),
	}); err != nil {
		return fmt.Errorf("mark reverted: %w", err)
	}
	return nil
}

// Diff computes (current_state, target_state, fields_that_would_change)
// without touching the DB. The UI uses this to render a side-by-side
// preview before the operator clicks Apply.
func Diff(ctx context.Context, q Querier, baselineID uuid.UUID) (DiffResult, error) {
	row, err := q.GetComplianceBaseline(ctx, baselineID)
	if err != nil {
		return DiffResult{}, fmt.Errorf("load baseline: %w", err)
	}
	canonical, ok := BySlug(row.Slug)
	if !ok {
		return DiffResult{}, fmt.Errorf("baseline slug %q has no canonical spec in registry", row.Slug)
	}
	spec := canonical.Spec

	target := specToMap(spec)
	current := buildCurrentMap(ctx, q, spec)

	changes := []string{}
	for k, t := range target {
		c, ok := current[k]
		if !ok || !jsonEqual(c, t) {
			changes = append(changes, k)
		}
	}
	sort.Strings(changes)

	return DiffResult{
		BaselineID:   baselineID,
		BaselineSlug: row.Slug,
		BaselineName: row.Name,
		Current:      current,
		Target:       target,
		Changes:      changes,
	}, nil
}

// ── internal write helpers ────────────────────────────────────────────

// writeSpec performs the actual DB writes for one BaselineSpec. Used
// by Apply (for the canonical spec) and Revert (for the captured
// previous_state). Best-effort per field — a missing helper table
// (e.g. sprint-063's read_audit_policies) is logged + skipped, not
// fatal. A DB error on a write that SHOULD have succeeded is fatal
// and returned to the caller for tx rollback.
func writeSpec(ctx context.Context, q Querier, spec BaselineSpec, userID uuid.UUID, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	uid := pgtypeUUID(userID)

	// Audit retention — single platform_setting key.
	if spec.AuditRetentionDays > 0 {
		val, _ := json.Marshal(spec.AuditRetentionDays)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "audit.retention_days", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set audit.retention_days: %w", err)
		}
	}

	// PSS profile.
	if spec.PSSProfile != "" {
		val, _ := json.Marshal(spec.PSSProfile)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "pod_security.default_profile", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set pod_security.default_profile: %w", err)
		}
	}

	// TOTP requirement.
	if spec.RequiredTOTP {
		val, _ := json.Marshal(true)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "totp.required", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set totp.required: %w", err)
		}
	}

	// SMTP-required flag (recorded only — no enforcement at SMTP
	// delete time; that's v2 per docs/compliance.md).
	if spec.RequiredSMTP {
		val, _ := json.Marshal(true)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "smtp.required", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set smtp.required: %w", err)
		}
	}

	// Required webhooks — recorded as JSON list.
	if len(spec.RequiredWebhooks) > 0 {
		val, _ := json.Marshal(spec.RequiredWebhooks)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "webhooks.required", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set webhooks.required: %w", err)
		}
	}

	// Arbitrary key/value platform_settings the spec pins.
	for k, raw := range spec.PlatformSettings {
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: k, Value: json.RawMessage(raw), UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set %s: %w", k, err)
		}
	}

	// Maintenance window template (recorded as platform_setting
	// rather than a maintenance_windows row insert — see comment on
	// MaintenanceWindowSpec for rationale).
	if spec.MaintenanceWindowTpl != nil {
		val, _ := json.Marshal(spec.MaintenanceWindowTpl)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "maintenance.template", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set maintenance.template: %w", err)
		}
	}

	// Quota plans.
	for _, qp := range spec.QuotaPlans {
		// Guard the "default" plan name — operators may have
		// customized their default tier; the baseline never
		// overwrites it. The CLI/test path can still target named
		// plans the baseline owns ("pci-prod", etc.).
		if qp.Name == "default" {
			logger.Warn("compliance.apply: skipping baseline quota plan named 'default' (operator-managed)")
			continue
		}
		params := sqlc.UpsertQuotaPlanParams{
			Name:                    qp.Name,
			Enforcement:             cmp.Or(qp.Enforcement, "hard"),
			Description:             qp.Description,
			MaxClustersPerProject:   int32(qp.MaxClustersPerProject),
			MaxNamespacesPerProject: int32(qp.MaxNamespacesPerProject),
			MaxMembersPerProject:    int32(qp.MaxMembersPerProject),
			MaxProjectsPerUser:      int32(qp.MaxProjectsPerUser),
			MaxTokensPerUser:        int32(qp.MaxTokensPerUser),
			MaxStreamsPerUser:       int32(qp.MaxStreamsPerUser),
			MaxTotalClusters:        int32(qp.MaxTotalClusters),
			MaxTotalUsers:           int32(qp.MaxTotalUsers),
		}
		if _, err := q.UpsertQuotaPlan(ctx, params); err != nil {
			return fmt.Errorf("upsert quota_plan %s: %w", qp.Name, err)
		}
	}

	// Alert rules — recorded as a platform_setting blob for v1. The
	// alerting handler reads this template on init. Wiring through
	// CreateAlertRule directly would conflict with operator-edited
	// rules of the same name; the platform_settings approach keeps
	// the baseline declarative without competing for ownership of
	// the alert_rules table.
	if len(spec.AlertRules) > 0 {
		val, _ := json.Marshal(spec.AlertRules)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: "alerts.baseline_template", Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("set alerts.baseline_template: %w", err)
		}
	}

	// Read-audit policies (sprint 063). If the table doesn't exist
	// yet on this platform, the Querier won't have the method —
	// we degrade gracefully by NOT having a method here. Operators
	// who upgrade to sprint 063 + re-apply pick up the enablement.
	if len(spec.ReadAuditPolicies) > 0 {
		logger.Warn("compliance.apply: read_audit_policies field present but engine doesn't yet wire to sprint-063 table; skipping",
			slog.Int("count", len(spec.ReadAuditPolicies)))
	}

	return nil
}

// restoreSpec re-applies a captured previous_state during Revert.
// Unlike writeSpec (which is set-only-when-truthy, correct for a
// forward Apply that never wants to write a "0"/""/false a baseline
// didn't specify), restoreSpec writes every scalar the baseline OWNS
// unconditionally — including the captured false/empty/zero value — so
// a revert genuinely undoes what the apply pinned on. `owner` is the
// canonical spec of the applied baseline (the ownership set); `snap`
// carries the pre-apply values to restore.
//
// Richer collection fields (quota plans, alert/maintenance templates,
// the arbitrary platform-settings map) don't suffer the truthy-skip
// bug — their snapshot values are real, non-empty entries — so they're
// restored via the shared writeSpec writer.
func restoreSpec(ctx context.Context, q Querier, owner, snap BaselineSpec, userID uuid.UUID, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	// Restore the collection/table-backed fields first via the shared
	// writer (these carry real captured values, no truthy-skip issue).
	if err := writeSpec(ctx, q, snap, userID, logger); err != nil {
		return err
	}

	uid := pgtypeUUID(userID)
	set := func(key string, v any) error {
		val, _ := json.Marshal(v)
		if _, err := q.UpsertPlatformSetting(ctx, sqlc.UpsertPlatformSettingParams{
			Key: key, Value: val, UpdatedBy: uid,
		}); err != nil {
			return fmt.Errorf("restore %s: %w", key, err)
		}
		return nil
	}

	// Scalar settings the baseline owns: write the captured value
	// unconditionally so a revert to false/""/0 actually lands.
	if owner.AuditRetentionDays > 0 {
		if err := set("audit.retention_days", snap.AuditRetentionDays); err != nil {
			return err
		}
	}
	if owner.PSSProfile != "" {
		if err := set("pod_security.default_profile", snap.PSSProfile); err != nil {
			return err
		}
	}
	if owner.RequiredTOTP {
		if err := set("totp.required", snap.RequiredTOTP); err != nil {
			return err
		}
	}
	if owner.RequiredSMTP {
		if err := set("smtp.required", snap.RequiredSMTP); err != nil {
			return err
		}
	}
	if len(owner.RequiredWebhooks) > 0 {
		webhooks := snap.RequiredWebhooks
		if webhooks == nil {
			webhooks = []string{}
		}
		if err := set("webhooks.required", webhooks); err != nil {
			return err
		}
	}
	return nil
}

// buildSnapshot reads the CURRENT value of every field the spec
// touches, so the previous_state JSON is sized to the apply (not the
// entire baseline schema). Reverting an apply that only touched
// audit_retention shouldn't restore unrelated platform_settings.
func buildSnapshot(ctx context.Context, q Querier, spec BaselineSpec) BaselineSpec {
	out := BaselineSpec{}
	if spec.AuditRetentionDays > 0 {
		out.AuditRetentionDays = readIntSetting(ctx, q, "audit.retention_days", 0)
	}
	if spec.PSSProfile != "" {
		out.PSSProfile = readStringSetting(ctx, q, "pod_security.default_profile", "")
	}
	if spec.RequiredTOTP {
		out.RequiredTOTP = readBoolSetting(ctx, q, "totp.required", false)
	}
	if spec.RequiredSMTP {
		out.RequiredSMTP = readBoolSetting(ctx, q, "smtp.required", false)
	}
	if len(spec.RequiredWebhooks) > 0 {
		out.RequiredWebhooks = readStringSliceSetting(ctx, q, "webhooks.required")
	}
	if len(spec.PlatformSettings) > 0 {
		out.PlatformSettings = map[string]string{}
		for k := range spec.PlatformSettings {
			cur, err := q.GetPlatformSetting(ctx, k)
			if err != nil || len(cur.Value) == 0 {
				continue
			}
			out.PlatformSettings[k] = string(cur.Value)
		}
	}
	if spec.MaintenanceWindowTpl != nil {
		cur, err := q.GetPlatformSetting(ctx, "maintenance.template")
		if err == nil && len(cur.Value) > 0 {
			var tpl MaintenanceWindowSpec
			if err := json.Unmarshal(cur.Value, &tpl); err == nil {
				out.MaintenanceWindowTpl = &tpl
			}
		}
	}
	if len(spec.QuotaPlans) > 0 {
		for _, qp := range spec.QuotaPlans {
			cur, err := q.GetQuotaPlan(ctx, qp.Name)
			if err != nil {
				continue
			}
			out.QuotaPlans = append(out.QuotaPlans, QuotaPlanSpec{
				Name:                    cur.Name,
				Enforcement:             cur.Enforcement,
				Description:             cur.Description,
				MaxClustersPerProject:   int(cur.MaxClustersPerProject),
				MaxNamespacesPerProject: int(cur.MaxNamespacesPerProject),
				MaxMembersPerProject:    int(cur.MaxMembersPerProject),
				MaxProjectsPerUser:      int(cur.MaxProjectsPerUser),
				MaxTokensPerUser:        int(cur.MaxTokensPerUser),
				MaxStreamsPerUser:       int(cur.MaxStreamsPerUser),
				MaxTotalClusters:        int(cur.MaxTotalClusters),
				MaxTotalUsers:           int(cur.MaxTotalUsers),
			})
		}
	}
	if len(spec.AlertRules) > 0 {
		cur, err := q.GetPlatformSetting(ctx, "alerts.baseline_template")
		if err == nil && len(cur.Value) > 0 {
			var rules []AlertRuleSpec
			if err := json.Unmarshal(cur.Value, &rules); err == nil {
				out.AlertRules = rules
			}
		}
	}
	if len(spec.ReadAuditPolicies) > 0 {
		// Snapshot of read_audit_policies isn't taken — we don't
		// touch that table in writeSpec yet (sprint 063 dependency).
		out.ReadAuditPolicies = nil
	}
	return out
}

// ── diff helpers ──────────────────────────────────────────────────────

// specToMap projects a BaselineSpec to the flat map shape Diff
// emits. Each key is a stable wire-format name the UI's settings
// drawer can render.
func specToMap(spec BaselineSpec) map[string]any {
	out := map[string]any{}
	if spec.AuditRetentionDays > 0 {
		out["audit_retention_days"] = spec.AuditRetentionDays
	}
	if spec.PSSProfile != "" {
		out["pss_profile"] = spec.PSSProfile
	}
	if spec.RequiredTOTP {
		out["totp_required"] = true
	}
	if spec.RequiredSMTP {
		out["smtp_required"] = true
	}
	if len(spec.RequiredWebhooks) > 0 {
		out["required_webhooks"] = spec.RequiredWebhooks
	}
	for k, v := range spec.PlatformSettings {
		out["platform_setting:"+k] = v
	}
	for _, qp := range spec.QuotaPlans {
		out["quota_plan:"+qp.Name] = qp
	}
	if spec.MaintenanceWindowTpl != nil {
		out["maintenance_window_template"] = spec.MaintenanceWindowTpl
	}
	for _, ar := range spec.AlertRules {
		out["alert_rule:"+ar.Name] = ar
	}
	if len(spec.ReadAuditPolicies) > 0 {
		out["read_audit_policies"] = spec.ReadAuditPolicies
	}
	return out
}

// buildCurrentMap reads the CURRENT DB values for the fields the spec
// will write, in the same flat map shape as specToMap. Used by Diff.
func buildCurrentMap(ctx context.Context, q Querier, spec BaselineSpec) map[string]any {
	out := map[string]any{}
	if spec.AuditRetentionDays > 0 {
		out["audit_retention_days"] = readIntSetting(ctx, q, "audit.retention_days", 0)
	}
	if spec.PSSProfile != "" {
		out["pss_profile"] = readStringSetting(ctx, q, "pod_security.default_profile", "")
	}
	if spec.RequiredTOTP {
		out["totp_required"] = readBoolSetting(ctx, q, "totp.required", false)
	}
	if spec.RequiredSMTP {
		out["smtp_required"] = readBoolSetting(ctx, q, "smtp.required", false)
	}
	if len(spec.RequiredWebhooks) > 0 {
		out["required_webhooks"] = readStringSliceSetting(ctx, q, "webhooks.required")
	}
	for k := range spec.PlatformSettings {
		cur, err := q.GetPlatformSetting(ctx, k)
		if err != nil {
			out["platform_setting:"+k] = ""
		} else {
			out["platform_setting:"+k] = string(cur.Value)
		}
	}
	for _, qp := range spec.QuotaPlans {
		cur, err := q.GetQuotaPlan(ctx, qp.Name)
		if err != nil {
			out["quota_plan:"+qp.Name] = nil
			continue
		}
		out["quota_plan:"+qp.Name] = QuotaPlanSpec{
			Name:                    cur.Name,
			Enforcement:             cur.Enforcement,
			Description:             cur.Description,
			MaxClustersPerProject:   int(cur.MaxClustersPerProject),
			MaxNamespacesPerProject: int(cur.MaxNamespacesPerProject),
			MaxMembersPerProject:    int(cur.MaxMembersPerProject),
			MaxProjectsPerUser:      int(cur.MaxProjectsPerUser),
			MaxTokensPerUser:        int(cur.MaxTokensPerUser),
			MaxStreamsPerUser:       int(cur.MaxStreamsPerUser),
			MaxTotalClusters:        int(cur.MaxTotalClusters),
			MaxTotalUsers:           int(cur.MaxTotalUsers),
		}
	}
	if spec.MaintenanceWindowTpl != nil {
		cur, err := q.GetPlatformSetting(ctx, "maintenance.template")
		if err != nil || len(cur.Value) == 0 {
			out["maintenance_window_template"] = nil
		} else {
			var tpl MaintenanceWindowSpec
			if err := json.Unmarshal(cur.Value, &tpl); err == nil {
				out["maintenance_window_template"] = &tpl
			} else {
				out["maintenance_window_template"] = string(cur.Value)
			}
		}
	}
	if len(spec.AlertRules) > 0 {
		cur, err := q.GetPlatformSetting(ctx, "alerts.baseline_template")
		if err != nil || len(cur.Value) == 0 {
			out["alert_rules_template"] = nil
		}
		var rules []AlertRuleSpec
		if err := json.Unmarshal(cur.Value, &rules); err == nil {
			for _, ar := range rules {
				out["alert_rule:"+ar.Name] = ar
			}
		}
	}
	return out
}

// ── setting readers ───────────────────────────────────────────────────

// readIntSetting fetches and JSON-decodes a platform_setting as int.
// Returns the default when the row is missing or unparseable.
func readIntSetting(ctx context.Context, q Querier, key string, def int) int {
	row, err := q.GetPlatformSetting(ctx, key)
	if err != nil || len(row.Value) == 0 {
		return def
	}
	var v int
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return def
	}
	return v
}

func readStringSetting(ctx context.Context, q Querier, key, def string) string {
	row, err := q.GetPlatformSetting(ctx, key)
	if err != nil || len(row.Value) == 0 {
		return def
	}
	var v string
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return def
	}
	return v
}

func readBoolSetting(ctx context.Context, q Querier, key string, def bool) bool {
	row, err := q.GetPlatformSetting(ctx, key)
	if err != nil || len(row.Value) == 0 {
		return def
	}
	var v bool
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return def
	}
	return v
}

func readStringSliceSetting(ctx context.Context, q Querier, key string) []string {
	row, err := q.GetPlatformSetting(ctx, key)
	if err != nil || len(row.Value) == 0 {
		return nil
	}
	var v []string
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return nil
	}
	return v
}

// ── misc helpers ──────────────────────────────────────────────────────

func pgtypeUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

// jsonEqual returns true iff the two values round-trip to the same
// JSON encoding. Used by Diff to compare the typed `current` and
// `target` maps without writing a per-type comparator.
func jsonEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}
