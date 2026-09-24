export interface ContainerSpec {
  name?: string;
  image?: string;
  imagePullPolicy?: string;
  command?: string[];
  args?: string[];
  ports?: Array<{
    name?: string;
    containerPort?: number;
    protocol?: string;
  }>;
  env?: Array<{
    name?: string;
    value?: string;
    valueFrom?: Record<string, unknown>;
  }>;
  envFrom?: Array<Record<string, unknown>>;
  resources?: {
    requests?: Record<string, string>;
    limits?: Record<string, string>;
  };
  volumeMounts?: Array<{
    name?: string;
    mountPath?: string;
    readOnly?: boolean;
    subPath?: string;
  }>;
  readinessProbe?: Record<string, unknown>;
  livenessProbe?: Record<string, unknown>;
  startupProbe?: Record<string, unknown>;
  securityContext?: Record<string, unknown>;
}

export interface ContainerStateDetail {
  reason?: string;
  message?: string;
  exitCode?: number;
  signal?: number;
  startedAt?: string;
  finishedAt?: string;
}

export interface ContainerStatus {
  name?: string;
  image?: string;
  imageID?: string;
  containerID?: string;
  ready?: boolean;
  started?: boolean;
  restartCount?: number;
  state?: Record<string, ContainerStateDetail | undefined>;
  lastState?: Record<string, ContainerStateDetail | undefined>;
}

export interface K8sObject {
  apiVersion?: string;
  kind?: string;
  type?: string;
  metadata?: {
    name?: string;
    namespace?: string;
    creationTimestamp?: string;
    uid?: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    ownerReferences?: Array<{
      apiVersion?: string;
      kind: string;
      name: string;
      uid?: string;
    }>;
  };
  spec?: {
    nodeName?: string;
    containers?: ContainerSpec[];
    initContainers?: ContainerSpec[];
    ephemeralContainers?: ContainerSpec[];
    serviceAccountName?: string;
    priorityClassName?: string;
    priority?: number;
    restartPolicy?: string;
    schedulerName?: string;
    hostNetwork?: boolean;
    dnsPolicy?: string;
    imagePullSecrets?: Array<{ name?: string }>;
    volumes?: Array<Record<string, unknown> & { name?: string }>;
    tolerations?: Array<Record<string, unknown>>;
    nodeSelector?: Record<string, string>;
    affinity?: Record<string, unknown>;
    securityContext?: Record<string, unknown>;
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
    reason?: string;
    message?: string;
    podIP?: string;
    hostIP?: string;
    qosClass?: string;
    startTime?: string;
    containerStatuses?: ContainerStatus[];
    initContainerStatuses?: ContainerStatus[];
    ephemeralContainerStatuses?: ContainerStatus[];
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
