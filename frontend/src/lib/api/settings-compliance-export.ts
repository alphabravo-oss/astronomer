import { getAdminComplianceExport } from "@/lib/api/generated/client";

// ============================================================
// Types — Compliance
// ============================================================

// ============================================================
// Compliance — API funcs
// ============================================================

/**
 * Trigger or fetch a compliance export.
 *
 * The current backend streams the ZIP body directly with a 200 status.
 * The 202 branch remains for forward compatibility once durable background
 * export jobs exist.
 */
export async function requestComplianceExport(params: {
  from: string;
  to: string;
  signal?: AbortSignal;
}): Promise<{ blob: Blob; filename: string }> {
  const blob = await getAdminComplianceExport({
    query: { from: params.from, to: params.to },
    signal: params.signal,
  });
  return {
    blob: blob as unknown as Blob,
    filename: `astronomer-compliance-${params.from}-${params.to}.zip`,
  };
}

export async function downloadComplianceExportBlob(
  downloadUrl: string,
): Promise<Blob> {
  const res = await fetch(downloadUrl);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.blob();
}
