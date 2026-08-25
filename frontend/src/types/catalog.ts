import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// --- Catalog / Helm Types ---

export type HelmRepoType = "helm" | "oci";

type HelmRepositoryContract = CamelizeKeys<
  OpenAPIComponents["schemas"]["HelmRepository"]
>;

export type HelmRepository = Omit<
  HelmRepositoryContract,
  "repoType" | "lastSyncedAt" | "lastSyncAttemptedAt"
> & {
  id: string;
  name: string;
  url: string;
  repoType: HelmRepoType;
  description?: string;
  isDefault: boolean;
  enabled: boolean;
  chartCount: number;
  /** Last SUCCESSFUL ingest — a failed attempt deliberately does not advance it. */
  lastSyncedAt?: string;
  /** Last attempt, successful or not. Compare with lastSyncedAt to tell "failing" from "no longer visited". */
  lastSyncAttemptedAt?: string | null;
  /** Why the most recent sync attempt failed; empty when the last attempt succeeded. */
  lastSyncError?: string;
  createdAt: string;
  updatedAt: string;
};

export type HelmChartCategory =
  | "monitoring"
  | "logging"
  | "security"
  | "database"
  | "networking"
  | "storage"
  | "messaging"
  | "ci-cd"
  | "other";

type HelmChartContract = CamelizeKeys<
  OpenAPIComponents["schemas"]["HelmChart"]
>;

export type HelmChart = Omit<HelmChartContract, "category" | "keywords"> & {
  id: string;
  repositoryId: string;
  name: string;
  displayName: string;
  description?: string;
  iconUrl?: string;
  category: HelmChartCategory;
  keywords: string[];
  createdAt: string;
  updatedAt: string;
  /** UI enrichment when repository metadata is loaded alongside the chart page. */
  repositoryName?: string;
  /** Optional API enrichment; absence is rendered honestly instead of as `vundefined`. */
  latestVersion?: string;
};

type HelmChartVersionContract = CamelizeKeys<
  OpenAPIComponents["schemas"]["HelmChartVersion"]
>;

export type HelmChartVersion = HelmChartVersionContract & {
  id: string;
  chartId: string;
  version: string;
  appVersion: string;
  createdAt: string;
};

export type InstalledChartStatus = string;

type InstalledChartContract = CamelizeKeys<
  OpenAPIComponents["schemas"]["InstalledChart"]
>;

/** Honest fleet-list view; display names come from separate cluster/chart APIs. */
export type InstalledChart = Omit<InstalledChartContract, "status"> & {
  id: string;
  clusterId: string;
  chartVersionId?: string | null;
  releaseName: string;
  namespace: string;
  status: InstalledChartStatus;
  revision: number;
  createdAt: string;
  updatedAt: string;
};
