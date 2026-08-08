import type { CharlieDiagnosticCheck } from "@/lib/api/charlie-admin";
import type { User } from "@/types";
import { can } from "@/lib/permissions";

export const CHARLIE_ADMIN_TABS = [
  "connection",
  "agent",
  "mode",
  "alerts",
  "automation",
  "access",
  "diagnostics",
] as const;
export type CharlieAdminTab = (typeof CHARLIE_ADMIN_TABS)[number];

export function normalizeCharlieAdminTab(
  value: string | null,
): CharlieAdminTab {
  return CHARLIE_ADMIN_TABS.includes(value as CharlieAdminTab)
    ? (value as CharlieAdminTab)
    : "connection";
}

export function canManageCharlie(
  user: User | null | undefined,
): boolean {
  // Feature/connection state controls runtime authority, not access to the
  // local administration surface needed to inspect or enable it.
  return can(user, "charlie", "manage");
}

export function mergeCharlieSearch(
  current: URLSearchParams,
  updates: Record<string, string | undefined>,
): string {
  const next = new URLSearchParams(current);
  for (const [key, value] of Object.entries(updates)) {
    if (value) next.set(key, value);
    else next.delete(key);
  }
  return next.toString();
}

export function adjacentTab<T extends readonly string[]>(
  tabs: T,
  current: T[number],
  key: string,
): T[number] | null {
  const index = tabs.indexOf(current);
  if (index < 0) return null;
  if (key === "Home") return tabs[0];
  if (key === "End") return tabs[tabs.length - 1];
  if (key === "ArrowRight" || key === "ArrowDown")
    return tabs[(index + 1) % tabs.length];
  if (key === "ArrowLeft" || key === "ArrowUp")
    return tabs[(index - 1 + tabs.length) % tabs.length];
  return null;
}

export function parseOnboardingFile(text: string): Record<string, unknown> {
  if (new TextEncoder().encode(text).length > 1024 * 1024)
    throw new Error("Onboarding package exceeds 1 MiB");
  const value: unknown = JSON.parse(text);
  if (!value || Array.isArray(value) || typeof value !== "object")
    throw new Error("Onboarding package must be a JSON object");
  return value as Record<string, unknown>;
}

export function automationValidationIssues(input: {
  rules: Array<{
    name: string;
    sourceType: string;
    severities: string[];
    cooldownSeconds: number;
    gracePeriodSeconds: number;
    flapWindowSeconds: number;
    flapCount: number;
    fleetThresholdPercent: number;
    maximumAttempts: number;
    serviceIdentity: string;
  }>;
}): string[] {
  const issues: string[] = [];
  input.rules.forEach((rule, index) => {
    const label = rule.name.trim() || `Rule ${index + 1}`;
    if (!rule.name.trim()) issues.push(`Rule ${index + 1} needs a name.`);
    if (!rule.sourceType.trim()) issues.push(`${label} needs a source type.`);
    if (!rule.serviceIdentity.trim())
      issues.push(`${label} needs a service identity.`);
    if (rule.severities.length === 0)
      issues.push(`${label} needs at least one severity.`);
    if (
      [
        rule.cooldownSeconds,
        rule.gracePeriodSeconds,
        rule.flapWindowSeconds,
        rule.flapCount,
        rule.maximumAttempts,
      ].some((value) => !Number.isInteger(value) || value < 1)
    )
      issues.push(`${label} timing, flap count, and attempts must be positive integers.`);
    if (
      !Number.isInteger(rule.fleetThresholdPercent) ||
      rule.fleetThresholdPercent < 0 ||
      rule.fleetThresholdPercent > 100
    )
      issues.push(`${label} fleet threshold must be between 0 and 100.`);
  });
  return issues;
}

export const REQUIRED_DIAGNOSTICS: Array<
  Pick<CharlieDiagnosticCheck, "id" | "label">
> = [
  { id: "local_config", label: "Local database and configuration" },
  { id: "product_bridge_mtls", label: "Product Bridge mTLS" },
  { id: "agent_primary", label: "Agent primary replica" },
  { id: "agent_standby", label: "Agent standby replica" },
  { id: "central_via_agent", label: "Charlie central through agent" },
  { id: "leader_epoch", label: "Leader and fencing epoch" },
  { id: "route_rag", label: "Route and RAG readiness" },
  { id: "mcp_tls_discovery", label: "MCP TLS and discovery digest" },
  { id: "oci_artifacts", label: "Charlie OCI chart and image" },
  { id: "credential_expiry", label: "Certificate and credential expiry" },
];

export function completeDiagnostics(
  checks: CharlieDiagnosticCheck[],
): CharlieDiagnosticCheck[] {
  const byId = new Map(checks.map((check) => [check.id, check]));
  return REQUIRED_DIAGNOSTICS.map(
    (expected) =>
      byId.get(expected.id) ?? {
        ...expected,
        state: "unknown",
        summary: "Charlie did not report this check.",
      },
  );
}
