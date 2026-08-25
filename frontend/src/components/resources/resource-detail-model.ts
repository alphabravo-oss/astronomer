export interface ContainerSpec {
  name?: string;
  image?: string;
}

export interface ContainerStatus {
  name?: string;
  image?: string;
  ready?: boolean;
  restartCount?: number;
  state?: Record<string, { reason?: string } | undefined>;
}

export interface K8sObject {
  kind?: string;
  metadata?: {
    name?: string;
    namespace?: string;
    creationTimestamp?: string;
    uid?: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    ownerReferences?: Array<{ kind: string; name: string; uid?: string }>;
  };
  spec?: {
    nodeName?: string;
    containers?: ContainerSpec[];
    replicas?: number;
    paused?: boolean;
    suspend?: boolean;
    type?: string;
    clusterIP?: string;
    selector?: Record<string, string>;
    ports?: Array<{
      name?: string;
      port?: number;
      targetPort?: number | string;
      protocol?: string;
      nodePort?: number;
    }>;
    ingressClassName?: string;
    rules?: Array<{ host?: string }>;
    tls?: Array<{ hosts?: string[]; secretName?: string }>;
    storageClassName?: string;
    volumeName?: string;
    resources?: { requests?: { storage?: string } };
  };
  status?: {
    conditions?: Array<Record<string, unknown>>;
    phase?: string;
    podIP?: string;
    containerStatuses?: ContainerStatus[];
    capacity?: { storage?: string };
  };
  data?: Record<string, string>;
}

export function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
}

export function displayNumber(value: unknown, fallback = "-"): string {
  return value == null ? fallback : String(value);
}
