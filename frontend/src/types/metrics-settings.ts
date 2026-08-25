// --- Metrics Types ---

export interface TimeSeriesPoint {
  timestamp: string;
  value: number;
}

export interface MetricsSeries {
  name: string;
  label?: string;
  unit: string;
  data: TimeSeriesPoint[];
}

export interface MetricsData {
  // false when no per-cluster Prometheus/Thanos backend is configured, so the
  // series below are empty. The scalar /metrics/summary usage still resolves
  // from metrics-server independently of this flag.
  available?: boolean;
  cpuUsage: MetricsSeries;
  cpuCapacity: MetricsSeries;
  memoryUsage: MetricsSeries;
  memoryCapacity: MetricsSeries;
  networkReceive: MetricsSeries;
  networkTransmit: MetricsSeries;
  diskUsage: MetricsSeries;
  podCount: MetricsSeries;
}

export interface MetricsSummary {
  cpuUsage: number;
  cpuCapacity: number;
  cpuPercentage: number;
  memoryUsage: number;
  memoryCapacity: number;
  memoryPercentage: number;
  podCount: number;
  podCapacity: number;
  nodeCount: number;
  networkReceive: number;
  networkTransmit: number;
  diskUsage: number;
  diskCapacity: number;
}

// --- Settings Types ---

export interface SSOProvider {
  id: string;
  provider: string;
  type: "github" | "google" | "oidc";
  name: string;
  enabled: boolean;
  config: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

export interface APIToken {
  id: string;
  name: string;
  prefix: string;
  expiresAt?: string;
  lastUsedAt?: string;
  isRevoked: boolean;
  createdAt: string;
  scopes: string[];
  allowedCidrs?: string;
  lastSeenRemoteIp?: string;
}

export interface AuditLogEntry {
  id: string;
  userId?: string | null;
  action: string;
  // Migration 063 — action_class distinguishes read-side from mutation rows.
  actionClass?: "mutation" | "read" | "auth" | "system";
  resourceType: string;
  resourceId?: string;
  resourceName: string;
  source?: string;
  correlationId?: string;
  actorAuthMethod?: string;
  httpMethod?: string;
  path?: string;
  statusCode?: number;
  durationMs?: number;
  requestId?: string;
  ipAddress?: string | null;
  user: string;
  userAgent?: string;
  sourceIP: string;
  status: "success" | "failure" | "error";
  detail?: Record<string, unknown>;
  details?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
  timestamp: string;
}
