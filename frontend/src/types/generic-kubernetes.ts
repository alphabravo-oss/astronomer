// --- Generic K8s Resource ---

export interface GenericK8sResource {
  name: string;
  namespace: string;
  clusterId: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  createdAt: string;
  // Job fields
  completions?: number;
  succeeded?: number;
  failed?: number;
  active?: number;
  status?: string;
  // CronJob fields
  schedule?: string;
  suspend?: boolean;
  lastSchedule?: string;
  activeCount?: number;
  // ConfigMap/Secret fields
  dataCount?: number;
  type?: string;
  // HPA fields
  minReplicas?: number;
  maxReplicas?: number;
  currentReplicas?: number;
  desiredReplicas?: number;
  targetKind?: string;
  targetName?: string;
  // ResourceQuota fields
  hard?: Record<string, string>;
  used?: Record<string, string>;
  // LimitRange fields
  limits?: Array<Record<string, unknown>>;
  // PDB fields
  minAvailable?: string;
  maxUnavailable?: string;
  currentHealthy?: number;
  desiredHealthy?: number;
  disruptionsAllowed?: number;
  // CRD fields
  group?: string;
  kind?: string;
  scope?: string;
  version?: string;
  // ServiceAccount fields
  secretsCount?: number;
  // Role/ClusterRole fields
  rulesCount?: number;
  // RoleBinding/ClusterRoleBinding fields
  roleKind?: string;
  roleName?: string;
  subjectsCount?: number;
  // Endpoints fields
  addressesCount?: number;
  ports?: string;
  // ReplicaSet fields
  desired?: number;
  ready?: number;
  available?: number;
}
