// ============================================================
// Astronomer Platform Types
// ============================================================

import type { OpenAPIComponents } from "@/types/openapi.generated";

// --- API Response Types ---

export interface APIResponse<T> {
  data: T;
  message?: string;
  status: number;
}

export interface PaginatedResponse<T> {
  data: T[];
  pagination: PaginationMetadata;
}

export type PaginationMetadata =
  OpenAPIComponents["schemas"]["PaginationMetadata"];

export interface APIError {
  message: string;
  code: string;
  status: number;
  details?: Record<string, string>;
}

export * from "@/types/clusters";

export * from "@/types/workloads-projects";

export * from "@/types/identity-rbac";

export * from "@/types/metrics-settings";

export * from "@/types/alerting-security";

export * from "@/types/logging";

export * from "@/types/kubernetes-resources";

export * from "@/types/catalog";

export * from "@/types/backups";

export * from "@/types/security";

export * from "@/types/generic-kubernetes";

export * from "@/types/tools";

export * from "@/types/events";

export * from "@/types/dex";

export * from "@/types/velero";

export * from "@/types/cis";
