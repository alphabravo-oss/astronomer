/** Helpers shared by the control-plane snapshot list and restore guidance. */
const MANAGED_DISTRIBUTIONS = new Set(["eks", "aks", "gke"]);

export function isManagedControlPlane(distribution?: string): boolean {
  return distribution != null && MANAGED_DISTRIBUTIONS.has(distribution);
}

export function formatSnapshotBytes(bytes?: number): string {
  if (bytes == null) return "—";
  if (bytes < 1024) return `${bytes} B`;

  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

export function formatSnapshotDate(iso?: string): string {
  if (!iso) return "—";
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}
