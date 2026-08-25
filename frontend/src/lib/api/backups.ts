import {
  deleteBackupsSchedulesById,
  deleteBackupsStorageById,
  getBackups,
  getBackupsById,
  getBackupsRestores,
  getBackupsRestoresById,
  getBackupsSchedules,
  getBackupsSchedulesById,
  getBackupsStorage,
  getBackupsStorageById,
  postBackupsByIdRestore,
  postBackupsSchedules,
  postBackupsSchedulesByIdTriggerNow,
  postBackupsStorage,
  postBackupsStorageByIdTest,
  putBackupsSchedulesById,
  putBackupsStorageById,
} from "@/lib/api/generated/client";
import type {
  BackupRestore,
  BackupRun,
  BackupScheduleRow,
  BackupStatus,
  BackupStorageLocation,
  BackupStorageType,
  BackupType,
  CreateBackupStorageRequest,
  CreateRestoreRequestB2,
  CreateScheduleRequestB2,
  PaginatedResponse,
  TestStorageResult,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type BackupContracts = OpenAPIComponents["schemas"];
type StorageWire = BackupContracts["BackupStorageConfigResponse"];
type ScheduleWire = BackupContracts["BackupScheduleResponse"];
type BackupWire = BackupContracts["BackupResponse"];
type RestoreWire = BackupContracts["RestoreOperationResponse"];

interface PageInput {
  page?: number;
  page_size?: number;
}

function requiredString(
  value: string | null | undefined,
  field: string,
): string {
  if (!value) throw new Error(`Backup API response omitted ${field}`);
  return value;
}

function pageQuery(params?: PageInput): { limit?: number; offset?: number } {
  if (!params) return {};
  const limit = params.page_size;
  const page = Math.max(1, params.page ?? 1);
  return {
    limit,
    offset: limit === undefined ? undefined : (page - 1) * limit,
  };
}

function pageResponse<T>(
  response: {
    data?: T[];
    count?: number;
    next?: string | null;
    previous?: string | null;
  },
  params?: PageInput,
): PaginatedResponse<T> {
  const data = response.data ?? [];
  const total = response.count ?? data.length;
  const pageSize = params?.page_size ?? Math.max(data.length, 1);
  const page = Math.max(1, params?.page ?? 1);
  return {
    data,
    count: total,
    total,
    next: response.next ?? null,
    previous: response.previous ?? null,
    page,
    pageSize,
    totalPages: Math.max(1, Math.ceil(total / pageSize)),
  };
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function stringArray(value: unknown): string[] | null | undefined {
  if (value === null || value === undefined) return value;
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : undefined;
}

function stringRecord(
  value: unknown,
): Record<string, string> | null | undefined {
  if (value === null || value === undefined) return value;
  if (typeof value !== "object" || Array.isArray(value)) return undefined;
  const entries = Object.entries(value).filter(
    (entry): entry is [string, string] => typeof entry[1] === "string",
  );
  return Object.fromEntries(entries);
}

export function mapBackupStorage(wire: StorageWire): BackupStorageLocation {
  return {
    id: requiredString(wire.id, "storage.id"),
    name: requiredString(wire.name, "storage.name"),
    storageType: (wire.storage_type ?? "s3") as BackupStorageType,
    bucket: requiredString(wire.bucket, "storage.bucket"),
    prefix: wire.prefix,
    region: wire.region,
    endpointUrl: wire.endpoint_url,
    isDefault: wire.is_default ?? false,
    veleroNamespace: wire.velero_namespace,
    bslName: wire.bsl_name,
    hasCredentials: wire.has_credentials ?? false,
    clusterId: wire.cluster_id,
    createdAt: requiredString(wire.created_at, "storage.created_at"),
    updatedAt: requiredString(wire.updated_at, "storage.updated_at"),
  };
}

export function mapBackupSchedule(wire: ScheduleWire): BackupScheduleRow {
  return {
    id: requiredString(wire.id, "schedule.id"),
    name: requiredString(wire.name, "schedule.name"),
    storageId: requiredString(wire.storage_id, "schedule.storage_id"),
    backupType: (wire.backup_type ?? "full") as BackupType,
    cronExpression: requiredString(
      wire.cron_expression,
      "schedule.cron_expression",
    ),
    retentionCount: wire.retention_count ?? 0,
    enabled: wire.enabled ?? false,
    lastBackupId: wire.last_backup_id ?? undefined,
    clusterId: wire.cluster_id ?? undefined,
    veleroNamespace: wire.velero_namespace,
    veleroScheduleName: wire.velero_schedule_name,
    includedNamespaces: stringArray(wire.included_namespaces),
    excludedNamespaces: stringArray(wire.excluded_namespaces),
    ttl: wire.ttl,
    createdAt: requiredString(wire.created_at, "schedule.created_at"),
    updatedAt: requiredString(wire.updated_at, "schedule.updated_at"),
  };
}

export function mapBackupRun(wire: BackupWire): BackupRun {
  return {
    id: requiredString(wire.id, "backup.id"),
    name: requiredString(wire.name, "backup.name"),
    storageId: requiredString(wire.storage_id, "backup.storage_id"),
    backupType: (wire.backup_type ?? "full") as BackupType,
    status: (wire.status ?? "pending") as BackupStatus,
    filePath: wire.file_path,
    fileSizeBytes: wire.file_size_bytes,
    startedAt: wire.started_at ?? undefined,
    completedAt: wire.completed_at ?? undefined,
    errorMessage: wire.error_message,
    clusterId: wire.cluster_id ?? undefined,
    veleroBackupName: wire.velero_backup_name,
    veleroNamespace: wire.velero_namespace,
    includedNamespaces: stringArray(wire.included_namespaces),
    excludedNamespaces: stringArray(wire.excluded_namespaces),
    pollAttempts: wire.poll_attempts,
    lastPolledAt: wire.last_polled_at ?? undefined,
    createdById: wire.created_by_id ?? undefined,
    createdAt: requiredString(wire.created_at, "backup.created_at"),
    updatedAt: requiredString(wire.updated_at, "backup.updated_at"),
  };
}

export function mapBackupRestore(wire: RestoreWire): BackupRestore {
  return {
    id: requiredString(wire.id, "restore.id"),
    backupId: requiredString(wire.backup_id, "restore.backup_id"),
    status: (wire.status ?? "pending") as BackupStatus,
    startedAt: wire.started_at ?? undefined,
    completedAt: wire.completed_at ?? undefined,
    errorMessage: wire.error_message,
    clusterId: wire.cluster_id ?? undefined,
    veleroNamespace: wire.velero_namespace,
    veleroRestoreName: wire.velero_restore_name,
    includedNamespaces: stringArray(wire.included_namespaces),
    namespaceMapping: stringRecord(wire.namespace_mapping),
    pollAttempts: wire.poll_attempts,
    lastPolledAt: wire.last_polled_at ?? undefined,
    createdAt: requiredString(wire.created_at, "restore.created_at"),
    updatedAt: requiredString(wire.updated_at, "restore.updated_at"),
  };
}

export async function b2ListStorageLocations(
  params?: PageInput & { cluster_id?: string },
): Promise<PaginatedResponse<BackupStorageLocation>> {
  const response = await getBackupsStorage({ query: pageQuery(params) });
  const data = (response.data ?? []).map(mapBackupStorage);
  return pageResponse({ ...response, data }, params);
}

export async function b2GetStorageLocation(
  id: string,
): Promise<BackupStorageLocation> {
  return mapBackupStorage(
    requireData(await getBackupsStorageById({ path: { id } }), "get storage"),
  );
}

export async function b2CreateStorageLocation(
  body: CreateBackupStorageRequest,
): Promise<BackupStorageLocation> {
  return mapBackupStorage(
    requireData(await postBackupsStorage({ body }), "create storage"),
  );
}

export async function b2UpdateStorageLocation(
  id: string,
  body: Partial<CreateBackupStorageRequest>,
): Promise<BackupStorageLocation> {
  if (!body.name || !body.bucket) {
    throw new Error(
      "Storage replacement requires the current name and bucket; partial PUT is unsafe",
    );
  }
  return mapBackupStorage(
    requireData(
      await putBackupsStorageById({
        path: { id },
        body: { ...body, name: body.name, bucket: body.bucket },
      }),
      "update storage",
    ),
  );
}

export async function b2DeleteStorageLocation(id: string): Promise<void> {
  await deleteBackupsStorageById({ path: { id } });
}

export async function b2TestStorageLocation(
  id: string,
): Promise<TestStorageResult> {
  const wire = requireData(
    await postBackupsStorageByIdTest({ path: { id } }),
    "test storage",
  );
  return { success: wire.success ?? false, message: wire.message ?? "" };
}

export async function b2ListSchedules(
  params?: PageInput & { cluster_id?: string },
): Promise<PaginatedResponse<BackupScheduleRow>> {
  const response = await getBackupsSchedules({ query: pageQuery(params) });
  const data = (response.data ?? []).map(mapBackupSchedule);
  return pageResponse({ ...response, data }, params);
}

export async function b2GetSchedule(id: string): Promise<BackupScheduleRow> {
  return mapBackupSchedule(
    requireData(
      await getBackupsSchedulesById({ path: { id } }),
      "get schedule",
    ),
  );
}

function completeScheduleBody(
  body: Partial<CreateScheduleRequestB2>,
): BackupContracts["BackupScheduleRequest"] {
  if (!body.name || !body.storage_id || !body.cron_expression) {
    throw new Error(
      "Schedule replacement requires the current name, storage, and cron expression; partial PUT is unsafe",
    );
  }
  return {
    ...body,
    name: body.name,
    storage_id: body.storage_id,
    cron_expression: body.cron_expression,
  };
}

export async function b2CreateSchedule(
  body: CreateScheduleRequestB2,
): Promise<BackupScheduleRow> {
  return mapBackupSchedule(
    requireData(
      await postBackupsSchedules({ body: completeScheduleBody(body) }),
      "create schedule",
    ),
  );
}

export async function b2UpdateSchedule(
  id: string,
  body: Partial<CreateScheduleRequestB2>,
): Promise<BackupScheduleRow> {
  return mapBackupSchedule(
    requireData(
      await putBackupsSchedulesById({
        path: { id },
        body: completeScheduleBody(body),
      }),
      "update schedule",
    ),
  );
}

export async function b2DeleteSchedule(id: string): Promise<void> {
  await deleteBackupsSchedulesById({ path: { id } });
}

export async function b2TriggerScheduleNow(id: string): Promise<BackupRun> {
  return mapBackupRun(
    requireData(
      await postBackupsSchedulesByIdTriggerNow({ path: { id } }),
      "trigger schedule",
    ),
  );
}

export async function b2ListRuns(
  params?: PageInput & {
    cluster_id?: string;
    schedule_id?: string;
    status?: string;
  },
): Promise<PaginatedResponse<BackupRun>> {
  const response = await getBackups({ query: pageQuery(params) });
  const data = (response.data ?? []).map(mapBackupRun);
  return pageResponse({ ...response, data }, params);
}

export async function b2GetRun(id: string): Promise<BackupRun> {
  return mapBackupRun(
    requireData(await getBackupsById({ path: { id } }), "get backup"),
  );
}

export async function b2ListRestores(
  params?: PageInput,
): Promise<PaginatedResponse<BackupRestore>> {
  const response = await getBackupsRestores({ query: pageQuery(params) });
  const data = (response.data ?? []).map(mapBackupRestore);
  return pageResponse({ ...response, data }, params);
}

export async function b2GetRestore(id: string): Promise<BackupRestore> {
  return mapBackupRestore(
    requireData(await getBackupsRestoresById({ path: { id } }), "get restore"),
  );
}

export async function b2CreateRestore(
  body: CreateRestoreRequestB2,
): Promise<BackupRestore> {
  const { backup_id: id, restore_pvs: _restorePVs, ...requestBody } = body;
  return mapBackupRestore(
    requireData(
      await postBackupsByIdRestore({ path: { id }, body: requestBody }),
      "create restore",
    ),
  );
}
