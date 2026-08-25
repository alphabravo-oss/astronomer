import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// === Phase B5: CIS ===
//
// CIS scan types backed by `internal/handler/security.go`. The Go handler
// emits snake_case which the security-scans adapter maps on the way in, so
// these mirror the feature's camelCase view shape.

export type CISScanStatus =
  "pending" | "running" | "completed" | "failed" | string;

export type CISFindingSeverity =
  "critical" | "high" | "medium" | "low" | "info" | string;

export type CISFindingStatus =
  "pass" | "fail" | "warn" | "skip" | "info" | string;

/**
 * One row out of a `ClusterScanReport` after we've flattened it into the
 * cis-operator-agnostic finding shape the backend persists in JSONB.
 */
export type CISFinding = CamelizeKeys<
  OpenAPIComponents["schemas"]["CISFinding"]
>;

/** ClusterScanProfile entry returned by `/security/profiles/?cluster_id=X`. */
export type CISProfile = OpenAPIComponents["schemas"]["CISProfile"];

/** Full envelope so callers can distinguish operator-installed vs fallback. */
export type CISProfilesResponse = Omit<
  OpenAPIComponents["schemas"]["CISProfilesResponse"],
  "items"
> & {
  items: CISProfile[];
  /** `cluster` if the cis-operator was queried, `fallback` otherwise. */
  source: "cluster" | "fallback";
  /** Populated when the cluster query failed and we returned the static set. */
  error?: string;
};

/**
 * Paginated list row. Findings are not included to keep the payload light;
 * fetch `getCISScan` for the full breakdown.
 */
export interface CISScanListItem {
  id: string;
  clusterId: string;
  scanType: string;
  status: CISScanStatus;
  passed: number;
  failed: number;
  warned: number;
  skipped: number;
  startedAt?: string;
  completedAt?: string | null;
  clusterScanName?: string;
  initiatedById?: string;
  errorMessage?: string;
  createdAt: string;
  updatedAt: string;
}

/** Full scan response from `GET /security/scans/{id}/`. */
export interface CISScanDetail extends CISScanListItem {
  findings: CISFinding[];
  /** Raw JSON from cis-operator — opaque to us; surfaced for debugging. */
  summary?: unknown;
  results?: unknown;
}

/** Body for `POST /security/scans/`. */
export interface CISScanCreatePayload {
  cluster_id: string;
  /** Defaults from `cluster.distribution` when omitted. */
  profile?: string;
}
