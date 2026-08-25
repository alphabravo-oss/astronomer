import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// --- Security Types ---

export type PodSecurityLevel = "privileged" | "baseline" | "restricted";

export type PodSecurityTemplate = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["PodSecurityTemplate"]>,
  "description" | "isBuiltin"
> & {
  description?: string;
  /**
   * Platform-owned starter template seeded by migration 107. Built-in
   * templates are delivered (and assignable to clusters) but cannot be
   * edited or deleted — the backend refuses those mutations with a 403.
   */
  isBuiltin?: boolean;
};

export type SecurityPolicySyncStatus =
  "synced" | "pending" | "failed" | "unknown";

export type ClusterSecurityPolicy = CamelizeKeys<
  OpenAPIComponents["schemas"]["ClusterSecurityPolicy"]
>;

export type SecurityScanType = "cis-benchmark" | "psa-audit";

export type SecurityScanStatus = "pending" | "running" | "completed" | "failed";

export interface SecurityScanCheckResult {
  checkId: string;
  description: string;
  status: "pass" | "fail" | "warn" | "info";
  severity: "critical" | "high" | "medium" | "low" | "info";
  remediation?: string;
  details?: string;
}

export interface SecurityScanSummary {
  total: number;
  passed: number;
  failed: number;
  warned: number;
  info: number;
}

export interface SecurityScanResult {
  id: string;
  cluster: string;
  clusterName: string;
  scanType: SecurityScanType;
  status: SecurityScanStatus;
  summary?: SecurityScanSummary;
  results?: SecurityScanCheckResult[];
  startedAt?: string;
  completedAt?: string;
  initiatedBy: string;
  createdAt: string;
}
