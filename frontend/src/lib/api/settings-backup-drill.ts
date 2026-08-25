import {
  deleteAdminManagementBackupDestinationsById,
  getAdminBackupDrill,
  getAdminBackupDrillHistory,
  getAdminManagementBackup,
  getAdminManagementBackupOperationById,
  postAdminManagementBackupDestinations,
  postAdminManagementBackupDestinationsByIdRun,
  postAdminManagementBackupDestinationsByIdTest,
  putAdminManagementBackupDestinationsById,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type { OperationSnapshot } from "@/lib/api/operation-polling";
import type { PaginatedResponse } from "@/types";
import type {
  BackupDrillLatestResponse,
  BackupDrillResult,
  ManagementBackupDeleteReceipt,
  ManagementBackupDestination,
  ManagementBackupDestinationWriteRequest,
  ManagementBackupJob,
  ManagementBackupOperation,
  ManagementBackupStatusResponse,
  ManagementCronJobStatus,
} from "@/types/openapi.generated";

export type BackupDrillStatus = "success" | "failure" | "partial" | "running";
export type ManagementBackupDestinationWrite =
  ManagementBackupDestinationWriteRequest;
export const MANAGEMENT_BACKUP_SECRET_SENTINEL = "<encrypted>";

export interface BackupDrillResultView {
  id: string;
  startedAt: string;
  finishedAt?: string;
  status: BackupDrillStatus;
  backupKey: string;
  schemaVersion?: number;
  errorMessage?: string;
  createdAt: string;
}

export interface BackupDrillLatestView {
  latest: BackupDrillResultView | null;
  latestSuccess: BackupDrillResultView | null;
  latestSuccessAgeSeconds: number | null;
}

export interface ManagementCronJobStatusView {
  name: string;
  schedule: string;
  suspended: boolean;
  lastScheduleTime?: string;
  lastSuccessfulTime?: string;
}

export interface ManagementBackupJobView {
  name: string;
  startTime?: string;
  completionTime?: string;
  succeeded: number;
  failed: number;
  active: number;
  durationSeconds?: number;
}

export interface ManagementBackupDestinationView {
  id: string;
  name: string;
  source: "ui" | "helm" | string;
  bucket: string;
  prefix: string;
  region: string;
  endpoint?: string;
  schedule: string;
  enabled: boolean;
  keepDaily: number;
  keepWeekly: number;
  keepMonthly: number;
  hasCredentials: boolean;
  accessKey?: string;
  secretKey?: string;
  cronjob?: ManagementCronJobStatusView;
  lastJob?: ManagementBackupJobView;
  readOnly: boolean;
  desiredGeneration?: number;
  appliedGeneration?: number;
  desiredState?: string;
  reconcileStatus?: string;
  lastError?: string;
}

export interface ManagementBackupStatusView {
  enabled: boolean;
  reason?: string;
  destinations: ManagementBackupDestinationView[];
  encryptionKeyBackup: { wrappingConfigured: boolean };
  drill?: ManagementCronJobStatusView;
  cronjob?: ManagementCronJobStatusView;
  destination?: {
    bucket: string;
    prefix: string;
    region: string;
    endpoint?: string;
  };
  retention?: { daily?: string; weekly?: string; monthly?: string };
  lastJob?: ManagementBackupJobView;
}

export interface ManagementBackupDeleteReceiptView {
  destinationId: string;
  generation: number;
  desiredState: "deleted";
  status: string;
}

export interface ManagementBackupOperationView extends OperationSnapshot {
  destinationId: string;
  operationType: string;
}

export interface BackupRequestOptions {
  signal?: AbortSignal;
}

function requireData<T>(data: T | undefined, operation: string): T {
  if (data === undefined) throw new Error(`${operation} response omitted data`);
  return data;
}

function mapDrillResult(wire: BackupDrillResult): BackupDrillResultView {
  return {
    id: wire.id,
    startedAt: wire.started_at,
    finishedAt: wire.finished_at ?? undefined,
    status: wire.status,
    backupKey: wire.backup_key,
    schemaVersion: wire.schema_version ?? undefined,
    errorMessage: wire.error_message || undefined,
    createdAt: wire.created_at,
  };
}

function mapDrillLatest(
  wire: BackupDrillLatestResponse,
): BackupDrillLatestView {
  return {
    latest: wire.latest ? mapDrillResult(wire.latest) : null,
    latestSuccess: wire.latest_success
      ? mapDrillResult(wire.latest_success)
      : null,
    latestSuccessAgeSeconds: wire.latest_success_age_seconds,
  };
}

function mapCronJob(
  wire: ManagementCronJobStatus,
): ManagementCronJobStatusView {
  return {
    name: wire.name,
    schedule: wire.schedule,
    suspended: wire.suspended,
    lastScheduleTime: wire.last_schedule_time,
    lastSuccessfulTime: wire.last_successful_time,
  };
}

function mapJob(wire: ManagementBackupJob): ManagementBackupJobView {
  return {
    name: wire.name,
    startTime: wire.start_time,
    completionTime: wire.completion_time,
    succeeded: wire.succeeded,
    failed: wire.failed,
    active: wire.active,
    durationSeconds: wire.duration_seconds,
  };
}

function mapDestination(
  wire: ManagementBackupDestination,
): ManagementBackupDestinationView {
  return {
    id: wire.id,
    name: wire.name,
    source: wire.source,
    bucket: wire.bucket,
    prefix: wire.prefix,
    region: wire.region,
    endpoint: wire.endpoint,
    schedule: wire.schedule,
    enabled: wire.enabled,
    keepDaily: wire.keep_daily,
    keepWeekly: wire.keep_weekly,
    keepMonthly: wire.keep_monthly,
    hasCredentials: wire.has_credentials,
    accessKey: wire.access_key,
    secretKey: wire.secret_key,
    cronjob: wire.cronjob ? mapCronJob(wire.cronjob) : undefined,
    lastJob: wire.last_job ? mapJob(wire.last_job) : undefined,
    readOnly: wire.read_only,
    desiredGeneration: wire.desired_generation,
    appliedGeneration: wire.applied_generation,
    desiredState: wire.desired_state,
    reconcileStatus: wire.reconcile_status,
    lastError: wire.last_error,
  };
}

function mapStatus(
  wire: ManagementBackupStatusResponse,
): ManagementBackupStatusView {
  return {
    enabled: wire.enabled,
    reason: wire.reason,
    destinations: (wire.destinations ?? []).map(mapDestination),
    encryptionKeyBackup: {
      wrappingConfigured: wire.encryption_key_backup.wrapping_configured,
    },
    drill: wire.drill ? mapCronJob(wire.drill) : undefined,
    cronjob: wire.cronjob ? mapCronJob(wire.cronjob) : undefined,
    destination: wire.destination,
    retention: wire.retention,
    lastJob: wire.last_job ? mapJob(wire.last_job) : undefined,
  };
}

function mapOperation(
  wire: ManagementBackupOperation,
): ManagementBackupOperationView {
  return {
    id: wire.id,
    status: wire.status,
    errorMessage: wire.error_message || undefined,
    destinationId: wire.destination_id,
    operationType: wire.operation_type,
  };
}

function mapDeleteReceipt(
  wire: ManagementBackupDeleteReceipt,
): ManagementBackupDeleteReceiptView {
  return {
    destinationId: wire.destination_id,
    generation: wire.generation,
    desiredState: wire.desired_state,
    status: wire.status,
  };
}

export async function getLatestBackupDrill(
  options: BackupRequestOptions = {},
): Promise<BackupDrillLatestView> {
  const response = await getAdminBackupDrill({ signal: options.signal });
  return mapDrillLatest(requireData(response.data, "Get latest backup drill"));
}

export async function listBackupDrillHistory(
  params?: { page?: number; page_size?: number },
  options: BackupRequestOptions = {},
): Promise<PaginatedResponse<BackupDrillResultView>> {
  const pageSize = params?.page_size ?? 20;
  const page = Math.max(1, params?.page ?? 1);
  const response = await getAdminBackupDrillHistory({
    query: { limit: pageSize, offset: (page - 1) * pageSize },
    signal: options.signal,
  });
  return {
    data: (response.data ?? []).map(mapDrillResult),
    total: response.count,
    count: response.count,
    next: response.next,
    previous: response.previous,
    page,
    pageSize,
    totalPages: pageSize > 0 ? Math.ceil(response.count / pageSize) : 0,
  };
}

export async function getManagementBackupStatus(
  options: BackupRequestOptions = {},
): Promise<ManagementBackupStatusView> {
  const response = await getAdminManagementBackup({ signal: options.signal });
  return mapStatus(requireData(response.data, "Get management backup status"));
}

export async function createManagementBackupDestination(
  body: ManagementBackupDestinationWrite,
  options: BackupRequestOptions = {},
): Promise<ManagementBackupDestinationView> {
  const response = await postAdminManagementBackupDestinations({
    body,
    signal: options.signal,
  });
  return mapDestination(
    requireData(response.data, "Create backup destination"),
  );
}

export async function updateManagementBackupDestination(
  id: string,
  body: ManagementBackupDestinationWrite,
  options: BackupRequestOptions = {},
): Promise<ManagementBackupDestinationView> {
  const response = await putAdminManagementBackupDestinationsById({
    path: { id },
    body,
    signal: options.signal,
  });
  return mapDestination(
    requireData(response.data, "Update backup destination"),
  );
}

export async function deleteManagementBackupDestination(
  id: string,
  options: BackupRequestOptions = {},
): Promise<ManagementBackupDeleteReceiptView> {
  const response = await deleteAdminManagementBackupDestinationsById({
    path: { id },
    headerParams: idempotencyHeaderParams(),
    signal: options.signal,
  });
  return mapDeleteReceipt(
    requireData(response.data, "Delete backup destination"),
  );
}

export async function testManagementBackupDestination(
  id: string,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<ManagementBackupOperationView> {
  const response = await postAdminManagementBackupDestinationsByIdTest({
    path: { id },
    headerParams: { "Idempotency-Key": options.idempotencyKey },
    signal: options.signal,
  });
  return mapOperation(requireData(response.data, "Test backup destination"));
}

export async function runManagementBackupDestination(
  id: string,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<ManagementBackupOperationView> {
  const response = await postAdminManagementBackupDestinationsByIdRun({
    path: { id },
    headerParams: { "Idempotency-Key": options.idempotencyKey },
    signal: options.signal,
  });
  return mapOperation(requireData(response.data, "Run backup destination"));
}

export async function getManagementBackupOperation(
  id: string,
  signal?: AbortSignal,
): Promise<ManagementBackupOperationView> {
  const response = await getAdminManagementBackupOperationById({
    path: { id },
    signal,
  });
  return mapOperation(requireData(response.data, "Get backup operation"));
}
