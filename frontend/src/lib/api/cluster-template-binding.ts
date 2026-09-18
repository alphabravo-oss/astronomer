/** Cluster-template binding and reconciliation controls. */

import * as generated from "@/lib/api/generated/client";
import { requireEnvelopeData } from "@/lib/api/data-envelope";
import { createIdempotencyKey } from "@/lib/api/idempotency";

// ============================================================
// Cluster template binding
// ============================================================

export type ClusterTemplateStatus =
  "pending" | "applying" | "applied" | "failed" | string;

/**
 * Shape returned by `GET /clusters/{id}/template/` — the *binding* between a
 * cluster and the template it has applied. The template type itself (with
 * the editable spec) lives in ./project-detail.ts; importing it here would
 * create a circular re-export, so callers that need both pull each from its
 * source module.
 */
export interface ClusterTemplateBinding {
  templateId: string;
  templateName: string;
  templateDisplayName: string;
  status: ClusterTemplateStatus;
  appliedAt?: string;
  lastError?: string;
  spec: unknown;
}

function mapClusterTemplateBinding(
  wire: Record<string, unknown>,
): ClusterTemplateBinding {
  return {
    templateId: String(wire.template_id ?? ""),
    templateName: String(wire.template_name ?? ""),
    templateDisplayName: String(wire.template_name ?? ""),
    status: String(wire.status ?? ""),
    appliedAt: wire.applied_at ? String(wire.applied_at) : undefined,
    lastError: wire.last_error ? String(wire.last_error) : undefined,
    spec: wire.spec_snapshot,
  };
}

export async function getClusterTemplateBinding(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterTemplateBinding | null> {
  try {
    const wire = await generated.getClustersByClusterIdTemplate({
      path: { cluster_id: clusterId },
      signal,
    });
    return wire.data ? mapClusterTemplateBinding(wire.data) : null;
  } catch (err) {
    // 404 — no template bound. Anything else surfaces to the caller.
    const status = (err as { response?: { status?: number } })?.response
      ?.status;
    if (status === 404) return null;
    throw err;
  }
}

export async function bindClusterTemplate(
  clusterId: string,
  body: { template_id: string },
  signal?: AbortSignal,
): Promise<ClusterTemplateBinding> {
  const wire = await generated.postClustersByClusterIdTemplate({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    body,
    signal,
  });
  return mapClusterTemplateBinding(
    requireEnvelopeData(wire, "Template bind"),
  );
}

export async function reapplyClusterTemplate(
  clusterId: string,
  signal?: AbortSignal,
): Promise<ClusterTemplateBinding> {
  const wire = await generated.postClustersByClusterIdTemplateReapply({
    path: { cluster_id: clusterId },
    headerParams: { "Idempotency-Key": createIdempotencyKey() },
    signal,
  });
  return mapClusterTemplateBinding(
    requireEnvelopeData(wire, "Template reapply"),
  );
}

export async function detachClusterTemplate(
  clusterId: string,
  signal?: AbortSignal,
): Promise<void> {
  await generated.deleteClustersByClusterIdTemplate({
    path: { cluster_id: clusterId },
    signal,
  });
}
