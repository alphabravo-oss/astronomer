import type { CharlieFinding } from "@/lib/api/charlie";

const importantSeverity: Record<string, boolean> = {
  medium: true,
  warning: true,
  high: true,
  critical: true,
};

const inertBlockCodes = new Set([
  "feature_disabled",
  "connection_inactive",
  "emergency_disabled",
  "mode_disabled",
  "product_disabled",
  "deployment_disabled",
]);

export function selectImportantCharlieFindings(
  findings: readonly CharlieFinding[],
  limit = Number.MAX_SAFE_INTEGER,
): CharlieFinding[] {
  if (limit <= 0) return [];
  const selected = new Map<string, CharlieFinding>();
  for (const finding of findings) {
    if (
      (finding.state !== "open" && finding.state !== "acknowledged") ||
      !importantSeverity[finding.severity] ||
      inertBlockCodes.has(finding.reasonNoAction ?? "") ||
      selected.has(finding.id)
    ) {
      continue;
    }
    selected.set(finding.id, finding);
    if (selected.size >= limit) break;
  }
  return [...selected.values()];
}
