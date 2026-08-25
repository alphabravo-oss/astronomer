// --- Backup Types ---

export type BackupStorageType = "s3" | "gcs" | "azure" | "minio";

export interface BackupStorageConfig {
  id: string;
  name: string;
  storageType: BackupStorageType;
  bucket: string;
  prefix?: string;
  region?: string;
  endpointUrl?: string;
  isDefault: boolean;
  createdAt: string;
  updatedAt: string;
}

export type BackupType = "full" | "database" | "config";

export type BackupStatus = "pending" | "in_progress" | "completed" | "failed";

export interface Backup {
  id: string;
  name: string;
  storage: string;
  storageName: string;
  backupType: BackupType;
  status: BackupStatus;
  filePath?: string;
  fileSizeBytes?: number;
  startedAt?: string;
  completedAt?: string;
  errorMessage?: string;
  createdBy: string;
  createdAt: string;
}

export interface BackupSchedule {
  id: string;
  name: string;
  storage: string;
  storageName: string;
  backupType: BackupType;
  cronExpression: string;
  retentionCount: number;
  enabled: boolean;
  lastBackup?: string;
  lastBackupStatus?: BackupStatus;
  createdAt: string;
  updatedAt: string;
}
