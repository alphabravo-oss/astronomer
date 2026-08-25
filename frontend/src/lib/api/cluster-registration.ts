import {
  executeOpenAPIOperationWithResponse,
  getClustersByIdRegistrationStatus,
  postClustersByIdRegistrationCancel,
  postClustersByIdRegistrationConfirm,
  postClustersByIdRegistrationRetryByStepId,
  putClustersByIdRegistrationOptions,
} from "@/lib/api/generated/client";
import type {
  RegistrationStatus as RegistrationStatusWire,
  RegistrationStep as RegistrationStepWire,
} from "@/types/openapi.generated";

export type RegistrationPhase = RegistrationStatusWire["phase"];

export interface RegistrationStepView {
  id: string;
  stepName: string;
  label: string;
  status: RegistrationStepWire["status"];
  progressPct: number;
  detail?: Record<string, unknown>;
  startedAt?: string | null;
  completedAt?: string | null;
  errorMessage?: string;
  stepOrder: number;
  createdAt: string;
}

export interface RegistrationStatusView {
  clusterId: string;
  phase: RegistrationPhase;
  installBaseline?: boolean | null;
  startedAt?: string | null;
  completedAt?: string | null;
  steps: RegistrationStepView[];
}

export interface RegistrationRequestOptions {
  signal?: AbortSignal;
}

function mapRegistrationStep(wire: RegistrationStepWire): RegistrationStepView {
  return {
    id: wire.id,
    stepName: wire.step_name,
    label: wire.label,
    status: wire.status,
    progressPct: wire.progress_pct,
    detail: wire.detail,
    startedAt: wire.started_at,
    completedAt: wire.completed_at,
    errorMessage: wire.error_message,
    stepOrder: wire.step_order,
    createdAt: wire.created_at,
  };
}

function requireRegistrationStatus(
  wire: RegistrationStatusWire | undefined,
): RegistrationStatusView {
  if (!wire) throw new Error("Registration response omitted data");
  return {
    clusterId: wire.cluster_id,
    phase: wire.phase,
    installBaseline: wire.install_baseline,
    startedAt: wire.started_at,
    completedAt: wire.completed_at,
    steps: wire.steps.map(mapRegistrationStep),
  };
}

export async function getRegistrationStatus(
  clusterId: string,
  options: RegistrationRequestOptions = {},
): Promise<RegistrationStatusView> {
  const response = await getClustersByIdRegistrationStatus({
    path: { id: clusterId },
    signal: options.signal,
  });
  return requireRegistrationStatus(response.data);
}

export async function setRegistrationOptions(
  clusterId: string,
  installBaseline: boolean,
  options: RegistrationRequestOptions = {},
): Promise<RegistrationStatusView> {
  const response = await putClustersByIdRegistrationOptions({
    path: { id: clusterId },
    body: { install_baseline: installBaseline },
    signal: options.signal,
  });
  return requireRegistrationStatus(response.data);
}

export async function confirmRegistration(
  clusterId: string,
  options: RegistrationRequestOptions = {},
): Promise<RegistrationStatusView> {
  const response = await postClustersByIdRegistrationConfirm({
    path: { id: clusterId },
    signal: options.signal,
  });
  return requireRegistrationStatus(response.data);
}

export async function retryRegistrationStep(
  clusterId: string,
  stepId: string,
  options: RegistrationRequestOptions = {},
): Promise<RegistrationStatusView> {
  const response = await postClustersByIdRegistrationRetryByStepId({
    path: { id: clusterId, step_id: stepId },
    signal: options.signal,
  });
  return requireRegistrationStatus(response.data);
}

export async function cancelRegistration(
  clusterId: string,
  options: RegistrationRequestOptions = {},
): Promise<RegistrationStatusView> {
  const response = await postClustersByIdRegistrationCancel({
    path: { id: clusterId },
    signal: options.signal,
  });
  return requireRegistrationStatus(response.data);
}

async function fetchClusterManifest(
  clusterId: string,
  options: RegistrationRequestOptions,
) {
  return executeOpenAPIOperationWithResponse("getClustersByIdManifest", {
    path: { id: clusterId },
    signal: options.signal,
  });
}

export async function getClusterManifest(
  clusterId: string,
  options: RegistrationRequestOptions = {},
): Promise<string> {
  const response = await fetchClusterManifest(clusterId, options);
  return response.data;
}

export async function getClusterManifestWithToken(
  clusterId: string,
  options: RegistrationRequestOptions = {},
): Promise<{ manifest: string; token: string }> {
  const response = await fetchClusterManifest(clusterId, options);
  const token = String(
    response.headers["x-astronomer-registration-token"] ?? "",
  );
  return { manifest: response.data, token };
}
