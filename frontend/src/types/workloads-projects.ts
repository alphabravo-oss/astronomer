// --- Workload Types ---

export type WorkloadKind =
  "Deployment" | "StatefulSet" | "DaemonSet" | "Job" | "CronJob" | "ReplicaSet";

export type WorkloadStatus =
  "Running" | "Pending" | "Failed" | "Succeeded" | "Unknown";

export interface Workload {
  name: string;
  namespace: string;
  kind: WorkloadKind;
  clusterId: string;
  clusterName: string;
  status: WorkloadStatus;
  ready: string; // e.g., "3/3"
  upToDate: number;
  available: number;
  replicas: number;
  desiredReplicas: number;
  images: string[];
  labels: Record<string, string>;
  annotations: Record<string, string>;
  createdAt: string;
  age: string;
}

export type PodPhase =
  "Running" | "Pending" | "Succeeded" | "Failed" | "Unknown";

export interface Pod {
  name: string;
  namespace: string;
  clusterId: string;
  phase: PodPhase;
  status: string;
  ready: string;
  restarts: number;
  node: string;
  ip: string;
  containers: Container[];
  conditions: PodCondition[];
  createdAt: string;
  age: string;
}

export interface Container {
  name: string;
  image: string;
  status: "running" | "waiting" | "terminated";
  ready: boolean;
  restartCount: number;
  cpuRequest?: string;
  cpuLimit?: string;
  memoryRequest?: string;
  memoryLimit?: string;
  ports?: ContainerPort[];
}

export interface ContainerPort {
  name?: string;
  containerPort: number;
  protocol: string;
}

export interface PodCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransition: string;
}

export interface PodLog {
  timestamp: string;
  message: string;
  container: string;
  level?: "info" | "warn" | "error" | "debug";
}

// --- Namespace Types ---

export interface Namespace {
  name: string;
  clusterId: string;
  status: "Active" | "Terminating";
  labels: Record<string, string>;
  annotations: Record<string, string>;
  podCount: number;
  cpuUsage: number;
  cpuLimit: number;
  memoryUsage: number;
  memoryLimit: number;
  createdAt: string;
}

// --- Project Types ---

export interface Project {
  id: string;
  name: string;
  displayName: string;
  description?: string;
  // The Go backend models projects as cluster-scoped — one cluster per
  // project — so the canonical key is `cluster_id` (singular). The
  // old `clusterIds` array was a vestige from an earlier multi-cluster
  // design; kept here as optional for backward compatibility with any
  // legacy components that haven't been migrated yet.
  clusterId?: string;
  clusterIds?: string[];
  namespaces: string[];
  /** Loaded by the project RBAC detail API, not project CRUD/list responses. */
  members?: ProjectMember[];
  resourceQuota?: ResourceQuota;
  createdAt: string;
  updatedAt: string;
}

export interface ProjectMember {
  userId: string;
  username: string;
  role: string;
}

export interface ResourceQuota {
  cpuLimit: string;
  memoryLimit: string;
  podLimit: number;
  storageLimit: string;
}
