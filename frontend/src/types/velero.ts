import type {
  BackupStatus,
  BackupStorageType,
  BackupType,
} from "@/types/backups";

// === Phase B2: Velero backups ===
//
// Wire shapes for the Velero-backed endpoints under `/api/v1/backups/`. The
// Go handler emits snake_case JSON, and the backups API adapter maps incoming
// keys to camelCase, so the
// read-side shapes below use camelCase. Request bodies stay snake_case to
// match the Go `json:` tags exactly.

export type VeleroPhase =
  | "New"
  | "InProgress"
  | "Completed"
  | "PartiallyFailed"
  | "Failed"
  | "FailedValidation"
  | "Deleting"
  | "Finalizing"
  | string;

/** Storage location row as returned by `/backups/storage/`. Mirrors
 *  `BackupHandler.storageResponse` — credentials never round-trip; only the
 *  `hasCredentials` flag indicates whether a Fernet-sealed secret is on
 *  file. */
export interface BackupStorageLocation {
  id: string;
  name: string;
  storageType: BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpointUrl?: string;
  isDefault: boolean;
  veleroNamespace?: string;
  bslName?: string;
  hasCredentials: boolean;
  clusterId?: string;
  createdAt: string;
  updatedAt: string;
}

/** Schedule row as returned by `/backups/schedules/`. Included/excluded
 *  namespace columns are stored as JSON in postgres and arrive as parsed
 *  arrays after the backups adapter's explicit mapping pass. */
export interface BackupScheduleRow {
  id: string;
  name: string;
  storageId: string;
  backupType: BackupType;
  cronExpression: string;
  retentionCount: number;
  enabled: boolean;
  lastBackupId?: string;
  clusterId?: string;
  veleroNamespace?: string;
  veleroScheduleName?: string;
  includedNamespaces?: string[] | null;
  excludedNamespaces?: string[] | null;
  ttl?: string;
  createdAt: string;
  updatedAt: string;
}

/** A backup run (Velero `Backup` CR projection). Item counts and phase are
 *  populated by the controller-side reconciler — they may be undefined while
 *  the run is still being scheduled. */
export interface BackupRun {
  id: string;
  name: string;
  storageId: string;
  backupType: BackupType;
  status: BackupStatus;
  filePath?: string;
  fileSizeBytes?: number;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
  clusterId?: string;
  veleroBackupName?: string;
  veleroNamespace?: string;
  includedNamespaces?: string[] | null;
  excludedNamespaces?: string[] | null;
  pollAttempts?: number;
  lastPolledAt?: string;
  createdById?: string;
  createdAt: string;
  updatedAt: string;
  // Optional decorators the reconciler may project once known.
  phase?: VeleroPhase;
  itemsBackedUp?: number;
  totalItems?: number;
  warnings?: number;
  errors?: number;
}

export interface BackupRestore {
  id: string;
  backupId: string;
  status: BackupStatus;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
  clusterId?: string;
  veleroNamespace?: string;
  veleroRestoreName?: string;
  includedNamespaces?: string[] | null;
  namespaceMapping?: Record<string, string> | null;
  pollAttempts?: number;
  lastPolledAt?: string;
  createdAt: string;
  updatedAt: string;
  phase?: VeleroPhase;
  itemsRestored?: number;
  warnings?: number;
  errors?: number;
}

/** Wizard-only union. `s3-compatible` is a UI alias for the AWS plugin
 *  driving an arbitrary S3 endpoint (MinIO, Cloudflare R2, etc.); on the
 *  wire it serializes as `s3` with a populated `endpoint_url`. */
export type BackupBackendKind = "s3" | "gcs" | "azure" | "s3-compatible";

export interface CreateBackupStorageRequest {
  name: string;
  cluster_id?: string;
  storage_type: BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpoint_url?: string;
  access_key?: string;
  secret_key?: string;
  is_default?: boolean;
}

export interface TestStorageResult {
  success: boolean;
  message: string;
}

export interface CreateScheduleRequestB2 {
  name: string;
  storage_id: string;
  backup_type?: BackupType;
  cron_expression: string;
  included_namespaces?: string[];
  excluded_namespaces?: string[];
  ttl?: string;
  retention_count: number;
  enabled: boolean;
  cluster_id?: string;
}

export interface CreateRestoreRequestB2 {
  backup_id: string;
  included_namespaces?: string[];
  namespace_mapping?: Record<string, string>;
  restore_pvs?: boolean;
}
