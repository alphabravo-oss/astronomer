import {
  adminComplianceBaselineActive,
  adminComplianceBaselineApplicationRevert,
  adminComplianceBaselineApplicationsList,
  adminComplianceBaselineApply,
  adminComplianceBaselineDiff,
  adminComplianceBaselineGet,
  adminComplianceBaselinesList,
} from "@/lib/api/generated/client";
import type {
  ComplianceBaseline as ComplianceBaselineWire,
  ComplianceBaselineApplication as ComplianceBaselineApplicationWire,
  ComplianceBaselineApplyReceipt,
  ComplianceBaselineDiff as ComplianceBaselineDiffWire,
  ComplianceBaselineRevertReceipt,
  ComplianceBaselineSpec as ComplianceBaselineSpecWire,
} from "@/types/openapi.generated";

export type ComplianceBaselineSlug = ComplianceBaselineWire["slug"];
export type ComplianceBaselineSpecView = ComplianceBaselineSpecWire;

export interface ComplianceBaselineView {
  id: string;
  slug: ComplianceBaselineSlug;
  name: string;
  description: string;
  version: string;
  enabled: boolean;
  active: boolean;
  spec: ComplianceBaselineSpecView;
}

export interface ComplianceBaselineDiffView {
  baselineId: string;
  baselineSlug: string;
  baselineName: string;
  current: Record<string, unknown>;
  target: Record<string, unknown>;
  changes: string[];
}

export interface ComplianceBaselineApplicationView {
  id: string;
  baselineId: string;
  baselineSlug: string;
  baselineName: string;
  appliedBy?: string;
  appliedAt: string;
  status: "applied" | "reverted";
  revertedAt?: string;
  revertedBy?: string;
  notes: string;
  previousState?: unknown;
}

export interface ComplianceRequestOptions {
  signal?: AbortSignal;
}

function requireData<T>(data: T | undefined, operation: string): T {
  if (data === undefined) throw new Error(`${operation} response omitted data`);
  return data;
}

function mapBaseline(wire: ComplianceBaselineWire): ComplianceBaselineView {
  return {
    id: wire.id,
    slug: wire.slug,
    name: wire.name,
    description: wire.description,
    version: wire.version,
    enabled: wire.enabled,
    active: wire.active,
    spec: wire.spec,
  };
}

function mapApplication(
  wire: ComplianceBaselineApplicationWire,
): ComplianceBaselineApplicationView {
  return {
    id: wire.id,
    baselineId: wire.baseline_id,
    baselineSlug: wire.baseline_slug,
    baselineName: wire.baseline_name,
    appliedBy: wire.applied_by ?? undefined,
    appliedAt: wire.applied_at,
    status: wire.status,
    revertedAt: wire.reverted_at ?? undefined,
    revertedBy: wire.reverted_by ?? undefined,
    notes: wire.notes,
    previousState: wire.previous_state,
  };
}

function mapDiff(wire: ComplianceBaselineDiffWire): ComplianceBaselineDiffView {
  return {
    baselineId: wire.baseline_id,
    baselineSlug: wire.baseline_slug,
    baselineName: wire.baseline_name,
    current: wire.current,
    target: wire.target,
    changes: wire.changes,
  };
}

export async function listComplianceBaselines(
  options: ComplianceRequestOptions = {},
): Promise<ComplianceBaselineView[]> {
  const response = await adminComplianceBaselinesList({
    signal: options.signal,
  });
  return requireData(response.data, "List compliance baselines").map(
    mapBaseline,
  );
}

export async function getComplianceBaseline(
  id: string,
  options: ComplianceRequestOptions = {},
): Promise<ComplianceBaselineView> {
  const response = await adminComplianceBaselineGet({
    path: { id },
    signal: options.signal,
  });
  return mapBaseline(requireData(response.data, "Get compliance baseline"));
}

export async function getComplianceBaselineDiff(
  id: string,
  options: ComplianceRequestOptions = {},
): Promise<ComplianceBaselineDiffView> {
  const response = await adminComplianceBaselineDiff({
    path: { id },
    signal: options.signal,
  });
  return mapDiff(requireData(response.data, "Get compliance baseline diff"));
}

export async function applyComplianceBaseline(
  id: string,
  notes?: string,
  options: ComplianceRequestOptions = {},
): Promise<ComplianceBaselineApplyReceipt> {
  const response = await adminComplianceBaselineApply({
    path: { id },
    body: { notes: notes ?? "" },
    signal: options.signal,
  });
  return requireData(response.data, "Apply compliance baseline");
}

export async function getActiveComplianceBaseline(
  options: ComplianceRequestOptions = {},
): Promise<{ active: ComplianceBaselineApplicationView | null }> {
  const response = await adminComplianceBaselineActive({
    signal: options.signal,
  });
  const result = requireData(response.data, "Get active compliance baseline");
  return { active: result.active ? mapApplication(result.active) : null };
}

export async function listComplianceBaselineApplications(
  options: ComplianceRequestOptions = {},
): Promise<ComplianceBaselineApplicationView[]> {
  const response = await adminComplianceBaselineApplicationsList({
    signal: options.signal,
  });
  return requireData(response.data, "List compliance applications").map(
    mapApplication,
  );
}

export async function revertComplianceBaselineApplication(
  id: string,
  options: ComplianceRequestOptions = {},
): Promise<ComplianceBaselineRevertReceipt> {
  const response = await adminComplianceBaselineApplicationRevert({
    path: { id },
    signal: options.signal,
  });
  return requireData(response.data, "Revert compliance baseline");
}
