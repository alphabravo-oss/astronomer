/**
 * Settings hub API client — sprints 9–14.
 *
 * Covers the new admin surfaces: platform settings, SMTP, webhooks, quota
 * plans, SSO group mappings, compliance exports, and backup-restore drills.
 *
 * Conventions match the rest of `lib/api.ts`:
 *   - Generated reads preserve exact wire casing and feature modules map views.
 *   - Writes send the keys declared by the generated request contract.
 *   - Single-object endpoints return `{ data: T }`; lists return the standard
 *     paginated envelope `{ data, total, page, page_size, total_pages }`.
 *
 * All endpoints under `/api/v1/admin/*` require a JWT with the `admin` role
 * (or `is_superuser` claim). Cookie auth is handled by the shared transport;
 * admin gating in the UI is layered on top via `useIsSuperuser()`.
 */
export * from "@/lib/api/settings-email";

export * from "@/lib/api/settings-webhooks";

// Quota plans and usage use the generated OpenAPI boundary.
export * from "@/lib/api/quotas";
// Registry-backed platform settings use exact generated contracts.
export * from "@/lib/api/platform-settings";

export * from "@/lib/api/settings-group-mappings";

export * from "@/lib/api/settings-compliance-export";

export * from "@/lib/api/settings-backup-drill";

export * from "@/lib/api/settings-notification-templates";

// GitOps registration sources use the generated OpenAPI boundary.
export * from "@/lib/api/gitops";

export * from "@/lib/api/settings-read-audit-policies";

export * from "@/lib/api/settings-compliance-baselines";

export * from "@/lib/api/settings-network-policy-templates";
