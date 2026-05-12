// Package compliance — built-in baseline registry + apply/revert
// engine for migration 064's compliance_baselines feature.
//
// Sprint 17 packages the cumulative platform compliance knobs
// (audit retention, quota plans, pod security standard, maintenance
// windows, alert rules, platform_settings, webhook + SMTP + TOTP
// requirements, read-audit policies) into four named profiles
// (PCI-DSS 4.0 / HIPAA / FedRAMP-Moderate / SOC2) that an operator
// can apply in one click from /dashboard/settings/compliance/.
//
// The four canonical specs live in this file rather than as multi-KB
// JSON in the migration so:
//
//  1. Every spec is code-reviewable as Go (`git blame` answers "why
//     is HIPAA at 2190 days?") instead of as opaque JSON.
//  2. Auditors who read the source can find the per-control comment
//     trail in one place ("HIPAA §164.312(b) Audit Controls" etc.).
//  3. Adding a spec field is a Go struct change — no second migration
//     to populate the JSON.
//
// The migration seeds the four baseline ROWS (with empty `{}` spec)
// so the slugs are stable + UUID-addressable. The handler's GET
// joins those rows with this Registry() to surface the populated
// spec.
package compliance

// Baseline is one preset compliance profile. The DB carries the
// identifier columns (Slug/Name/Description/Version); Spec is the
// declarative bundle of mutations Apply() will write.
type Baseline struct {
	Slug        string       `json:"slug"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	Spec        BaselineSpec `json:"spec"`
}

// BaselineSpec is the on-the-wire JSON shape stored in the baseline's
// `spec` column and the application's `previous_state` column. Every
// field is omitempty so a previous_state snapshot only carries the
// keys the apply actually touched — keeps the JSON small and the
// diff readable.
//
// Adding a field: append it here, populate it in Registry() for the
// baselines that care, teach Apply() to write the matching DB rows,
// and bump the baseline's Version. Old applications keep working
// because previous_state JSON is forward-compatible (unknown keys
// just don't get restored on revert — operators see them in the
// audit detail).
type BaselineSpec struct {
	// AuditRetentionDays is the platform_settings.audit.retention_days
	// value Apply() writes. The handler refuses to apply a baseline
	// whose value is LOWER than the current setting — downgrading
	// audit retention is a destructive, compliance-relevant action
	// that should NEVER happen by mistake from a "one click apply".
	AuditRetentionDays int `json:"audit_retention_days"`

	// QuotaPlans is a list of named quota plans Apply() upserts.
	// Existing plans with the same name are OVERWRITTEN; plans with
	// other names are LEFT ALONE. Apply NEVER deletes plans. This
	// matters because the operator's `default` / `free` / `team` /
	// `enterprise` plans are part of their day-to-day tenant
	// onboarding and rewriting them silently would be a foot-gun.
	QuotaPlans []QuotaPlanSpec `json:"quota_plans,omitempty"`

	// PSSProfile is the Pod Security Standard label new namespaces
	// get by default ("restricted" | "baseline" | "privileged").
	// Apply writes this to platform_settings.pod_security.default_profile;
	// the actual per-namespace labelling is handled by the existing
	// namespace controller — Apply only sets the DEFAULT.
	PSSProfile string `json:"pss_profile,omitempty"`

	// MaintenanceWindowTpl: if non-nil, Apply inserts a maintenance
	// window template row. The template doesn't constrain operators
	// — they can edit / delete — but the baseline seeds it so a
	// fresh PCI install has the "Sun 02:00–04:00 UTC" window already
	// configured.
	MaintenanceWindowTpl *MaintenanceWindowSpec `json:"maintenance_window_template,omitempty"`

	// AlertRules: list of alert rules to upsert by name. Existing
	// alert rules with the same name are updated; ones with other
	// names are left alone (same conservative semantics as
	// QuotaPlans). Operators can disable a baseline-managed rule
	// afterwards if it's too noisy — the baseline doesn't re-enforce
	// on every request.
	AlertRules []AlertRuleSpec `json:"alert_rules,omitempty"`

	// PlatformSettings: arbitrary key→value-JSON map upserted into
	// platform_settings. Values are JSON-encoded strings (i.e. "15"
	// for an int, "\"info\"" for a string) since the underlying
	// column is JSONB. Apply validates the keys against the
	// settingsRegistry — unknown keys are logged + skipped instead
	// of failing the whole apply.
	PlatformSettings map[string]string `json:"platform_settings,omitempty"`

	// RequiredWebhooks: list of webhook subscription names the
	// baseline considers MANDATORY. v1: this is RECORDED ONLY — the
	// handler does NOT yet block webhook deletion if a baseline
	// requires it. See docs/compliance.md for the deferred follow-up.
	RequiredWebhooks []string `json:"required_webhooks,omitempty"`

	// RequiredSMTP signals "this baseline mandates SMTP be configured".
	// Apply doesn't configure SMTP itself (the operator's MX server
	// is environment-specific); it just records the requirement so
	// the UI can surface a "SMTP not configured — your applied
	// baseline requires it" warning.
	RequiredSMTP bool `json:"required_smtp,omitempty"`

	// RequiredTOTP signals "all users (incl. SSO) must have TOTP
	// enrolled". Apply writes platform_settings.totp.required=true.
	// Existing users without TOTP keep working (no forced lockout)
	// but new logins are nudged to enroll — see the auth handler's
	// TOTP gate.
	RequiredTOTP bool `json:"required_totp,omitempty"`

	// ReadAuditPolicies: names of read_audit_policies rows to flip
	// `enabled=true` on. Sprint 063 introduced that table; on a
	// platform where 063 hasn't been merged yet, Apply silently skips
	// this field (logged at warn level) rather than failing the
	// entire baseline apply.
	ReadAuditPolicies []string `json:"read_audit_policies,omitempty"`
}

// QuotaPlanSpec is the subset of quota_plans columns a baseline can
// pin. Apply UPSERTs by name. Missing fields keep the existing row's
// value (UPSERT semantics) so an operator can pre-tune a plan and
// have the baseline only fill the gaps.
type QuotaPlanSpec struct {
	Name                    string `json:"name"`
	Enforcement             string `json:"enforcement"` // "soft" | "hard"
	Description             string `json:"description,omitempty"`
	MaxClustersPerProject   int    `json:"max_clusters_per_project,omitempty"`
	MaxNamespacesPerProject int    `json:"max_namespaces_per_project,omitempty"`
	MaxMembersPerProject    int    `json:"max_members_per_project,omitempty"`
	MaxProjectsPerUser      int    `json:"max_projects_per_user,omitempty"`
	MaxTokensPerUser        int    `json:"max_tokens_per_user,omitempty"`
	MaxStreamsPerUser       int    `json:"max_streams_per_user,omitempty"`
	MaxTotalClusters        int    `json:"max_total_clusters,omitempty"`
	MaxTotalUsers           int    `json:"max_total_users,omitempty"`
}

// MaintenanceWindowSpec is the canonical "patching window" definition.
// Sprint 15 (migration 057) introduced the runtime table; for sprint
// 17, Apply writes the spec to platform_settings.maintenance.template
// — the actual maintenance_windows row insert is deferred to whenever
// the maintenance worker reads the template (so we don't ship a
// migration dependency between sprints).
type MaintenanceWindowSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	// DaysOfWeek: ISO weekdays, 1=Mon..7=Sun. Empty = every day.
	DaysOfWeek  []int    `json:"days_of_week,omitempty"`
	StartHour   int      `json:"start_hour"` // 0–23 UTC
	StartMinute int      `json:"start_minute"`
	DurationMin int      `json:"duration_min"`
}

// AlertRuleSpec mirrors the alert_rules columns Apply pins. The
// `configuration` field is a JSON blob the alerting engine
// interprets per rule_type; baselines populate it with the
// canonical thresholds for the control they're satisfying.
type AlertRuleSpec struct {
	Name            string            `json:"name"`
	RuleType        string            `json:"rule_type"`
	Severity        string            `json:"severity"`
	CooldownMinutes int               `json:"cooldown_minutes,omitempty"`
	Configuration   map[string]any    `json:"configuration"`
}

// Registry returns the four built-in baselines with their canonical
// specs. The handler's GET endpoints join the DB rows (slug → UUID)
// with the Spec returned here.
//
// Each baseline's comment block lists the controls it intends to
// satisfy. The mapping is intentionally informational — we're not
// claiming a third-party-attested implementation of any specific
// framework — but it documents the operator-facing intent of the
// preset.
func Registry() []Baseline {
	return []Baseline{
		pciDSS40(),
		hipaa(),
		fedRAMPModerate(),
		soc2(),
	}
}

// BySlug returns the canonical spec for a given baseline slug, or
// (Baseline{}, false) if the slug isn't a built-in. The handler uses
// this when joining one DB row's slug with its registry entry.
func BySlug(slug string) (Baseline, bool) {
	for _, b := range Registry() {
		if b.Slug == slug {
			return b, true
		}
	}
	return Baseline{}, false
}

// ── PCI-DSS 4.0 ───────────────────────────────────────────────────────
//
// Auditor-facing controls (informational mapping, not third-party-attested):
//
//   3.5.1  — Render PAN unreadable: enforced at the application layer
//            (out of scope for this baseline; not a platform-managed knob).
//   8.3.6  — MFA on all access: required_totp = true.
//   8.6.x  — Account lockout after failures: locked-accounts alert rule.
//   10.5.1 — Audit log retention >= 1 year: audit_retention_days = 365.
//   10.7.3 — Audit log review: read_audit_policies enabled.
//   12.10.x — Incident response: audit_log_sink webhook required.
//
// Numbers are PCI-DSS 4.0 references. Update the comment + the
// Version field when the underlying standard ticks.
func pciDSS40() Baseline {
	return Baseline{
		Slug:        "pci_dss_4_0",
		Name:        "PCI-DSS 4.0",
		Description: "Payment card industry — cardholder data scope",
		Version:     "1.0",
		Spec: BaselineSpec{
			// PCI 10.5.1 — one-year audit retention floor.
			AuditRetentionDays: 365,
			// PCI A2 / cloud guidance — restricted PSS matches the
			// "no privileged escalation, no root" cardholder zone.
			PSSProfile: "restricted",
			QuotaPlans: []QuotaPlanSpec{
				// Dedicated quota plan operators assign to projects
				// holding cardholder data so the noisy-neighbour
				// blast radius is bounded.
				{
					Name:                    "pci-prod",
					Enforcement:             "hard",
					Description:             "PCI-DSS production — hard caps, no soft-allow",
					MaxClustersPerProject:   50,
					MaxNamespacesPerProject: 200,
					MaxMembersPerProject:    100,
					MaxProjectsPerUser:      10,
					MaxTokensPerUser:        20,
					MaxStreamsPerUser:       10,
				},
			},
			AlertRules: []AlertRuleSpec{
				// PCI 8.6.x — repeated auth failures.
				{
					Name:            "pci-auth-failures",
					RuleType:        "auth_failures",
					Severity:        "warning",
					CooldownMinutes: 10,
					Configuration: map[string]any{
						"threshold": 10,
						"window":    "1m",
					},
				},
				// PCI 8.6.x — locked accounts.
				{
					Name:            "pci-locked-accounts",
					RuleType:        "locked_accounts",
					Severity:        "critical",
					CooldownMinutes: 15,
					Configuration: map[string]any{
						"threshold": 5,
						"window":    "1h",
					},
				},
			},
			PlatformSettings: map[string]string{
				// PCI 8.2.8 — re-authenticate after 15 minutes idle.
				"session.timeout_minutes": "15",
				// PCI 12.5.x — banner asserting in-scope environment.
				"banner.global_text":  `"PCI-DSS cardholder data environment — handling restricted to authorized personnel"`,
				"banner.global_color": `"warning"`,
			},
			RequiredWebhooks:  []string{"audit_log_sink"},
			RequiredSMTP:      true,
			RequiredTOTP:      true,
			ReadAuditPolicies: []string{"cloud_credentials_read", "audit_log_read", "platform_settings_read"},
		},
	}
}

// ── HIPAA ─────────────────────────────────────────────────────────────
//
// Auditor-facing controls (informational mapping):
//
//   §164.308(a)(1)  — Risk management: alerting + audit retention.
//   §164.308(a)(3)  — Workforce access: required_totp.
//   §164.312(a)(1)  — Access control: PSS restricted.
//   §164.312(b)     — Audit controls: 6-year audit retention.
//   §164.312(c)(1)  — Integrity: webhook export of audit log to a
//                    tamper-evident sink (encouraged, not enforced
//                    at the platform layer for v1).
//   §164.530(j)     — 6-year retention of policies & procedures.
//
// HIPAA's 6-year retention floor is the single biggest delta from
// the other baselines — 2190 days = 6 × 365.
func hipaa() Baseline {
	return Baseline{
		Slug:        "hipaa",
		Name:        "HIPAA",
		Description: "US healthcare — protected health information",
		Version:     "1.0",
		Spec: BaselineSpec{
			// HIPAA §164.530(j) — 6-year retention.
			AuditRetentionDays: 2190,
			// HIPAA §164.312(a)(1) — least-privilege execution.
			PSSProfile:   "restricted",
			RequiredTOTP: true,
			PlatformSettings: map[string]string{
				// HIPAA §164.312(a)(2)(iii) — auto-logoff (we use 15m
				// to align with PCI; HIPAA itself only requires
				// "automatic logoff", no specific timeout).
				"session.timeout_minutes": "15",
				// HIPAA §164.312(a)(2)(iv) — encryption: signal the
				// platform that encryption-at-rest is mandatory; the
				// existing storage-class checker reads this flag.
				"security.encryption_at_rest_required": "true",
				"banner.global_text":                   `"HIPAA — protected health information environment"`,
				"banner.global_color":                  `"warning"`,
			},
			ReadAuditPolicies: []string{"cloud_credentials_read", "audit_log_read"},
		},
	}
}

// ── FedRAMP Moderate ──────────────────────────────────────────────────
//
// Auditor-facing controls (informational mapping to NIST SP 800-53r5
// via the FedRAMP Moderate baseline catalog):
//
//   AC-7  — Unsuccessful logon attempts: lock-after-3 setting +
//           alert rule.
//   AC-11 — Session lock: session.timeout_minutes = 20 (moderate
//           baseline minimum; high-impact = 15).
//   AU-11 — Audit record retention: 3-year retention (1095 days).
//   IA-2(1) — MFA for privileged accounts: required_totp.
//   IA-2(2) — MFA for non-privileged accounts: required_totp.
//   SI-4  — System monitoring: all read_audit policies enabled.
//
// FedRAMP's 3-year retention sits between PCI (1 year) and HIPAA
// (6 years).
func fedRAMPModerate() Baseline {
	return Baseline{
		Slug:        "fedramp_moderate",
		Name:        "FedRAMP Moderate",
		Description: "US federal cloud — moderate-impact baseline",
		Version:     "1.0",
		Spec: BaselineSpec{
			// AU-11 — 3-year retention.
			AuditRetentionDays: 1095,
			PSSProfile:         "restricted",
			RequiredTOTP:       true,
			RequiredSMTP:       true,
			AlertRules: []AlertRuleSpec{
				// AC-7 — alert on repeated unsuccessful logons.
				{
					Name:            "fedramp-auth-failures",
					RuleType:        "auth_failures",
					Severity:        "warning",
					CooldownMinutes: 5,
					Configuration: map[string]any{
						"threshold": 3,
						"window":    "15m",
					},
				},
			},
			PlatformSettings: map[string]string{
				// AC-7 — lock after 3 failed logins.
				"auth.lockout_threshold": "3",
				// AC-11 — session lock at 20 minutes (FedRAMP Moderate).
				"session.timeout_minutes": "20",
				"banner.global_text":      `"FedRAMP-Moderate — US government workload"`,
				"banner.global_color":     `"info"`,
			},
			ReadAuditPolicies: []string{
				"cloud_credentials_read",
				"audit_log_read",
				"platform_settings_read",
				"webhook_secrets_read",
			},
		},
	}
}

// ── SOC 2 (Type II) ───────────────────────────────────────────────────
//
// Auditor-facing controls (informational mapping to the AICPA TSC):
//
//   CC6.1 — Logical access controls: required_smtp + baseline PSS.
//   CC6.6 — Restricted access: required_smtp for notification path.
//   CC7.2 — System monitoring: change-management maintenance windows.
//   CC7.5 — Operational availability: maintenance window template
//           gives the auditor a documented patching cadence.
//   CC8.1 — Change management: maintenance windows + audit retention.
//
// SOC2 is the least prescriptive of the four — operators looking for
// a "we have controls" baseline use it as a starting point and
// promote to PCI/HIPAA/FedRAMP later.
func soc2() Baseline {
	return Baseline{
		Slug:        "soc2",
		Name:        "SOC 2",
		Description: "Service organization controls (Type II)",
		Version:     "1.0",
		Spec: BaselineSpec{
			// AICPA TSC criteria around change-management retention.
			AuditRetentionDays: 365,
			// PSS baseline (not restricted) — SOC2 doesn't mandate
			// no-privileged-containers; operators upgrade to
			// restricted via PCI/HIPAA/FedRAMP if their controls
			// require it.
			PSSProfile:   "baseline",
			RequiredSMTP: true,
			// CC7.2 / CC7.5 — documented patching cadence.
			MaintenanceWindowTpl: &MaintenanceWindowSpec{
				Name:        "soc2-change-management",
				Description: "SOC 2 change-management window (Sun 02:00–04:00 UTC)",
				DaysOfWeek:  []int{7}, // Sunday
				StartHour:   2,
				StartMinute: 0,
				DurationMin: 120,
			},
			PlatformSettings: map[string]string{
				// CC6.6 — banner identifying the controlled environment.
				"banner.global_text":  `"SOC 2 controlled environment"`,
				"banner.global_color": `"info"`,
			},
		},
	}
}
