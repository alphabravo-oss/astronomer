// --- Storage Types ---

export interface PersistentVolume {
  name: string;
  clusterId: string;
  clusterName: string;
  status: "Available" | "Bound" | "Released" | "Failed";
  capacity: string;
  accessModes: string[];
  reclaimPolicy: string;
  storageClass: string;
  volumeMode: string;
  claimRef?: string;
  createdAt: string;
}

export interface PersistentVolumeClaim {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  status: "Pending" | "Bound" | "Lost";
  capacity: string;
  accessModes: string[];
  storageClass: string;
  volumeName?: string;
  createdAt: string;
}

export interface StorageClass {
  name: string;
  clusterId: string;
  clusterName: string;
  provisioner: string;
  reclaimPolicy: string;
  volumeBindingMode: string;
  allowVolumeExpansion: boolean;
  isDefault: boolean;
  parameters: Record<string, string>;
  createdAt: string;
}

// --- Networking Types ---

export interface K8sService {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  type: "ClusterIP" | "NodePort" | "LoadBalancer" | "ExternalName";
  clusterIP: string;
  externalIP?: string;
  ports: ServicePort[];
  selector: Record<string, string>;
  createdAt: string;
}

export interface ServicePort {
  name?: string;
  port: number;
  targetPort: number | string;
  protocol: string;
  nodePort?: number;
}

export interface Ingress {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  ingressClass?: string;
  hosts: string[];
  paths: IngressPath[];
  tls: boolean;
  createdAt: string;
}

export interface IngressPath {
  host: string;
  path: string;
  pathType: string;
  serviceName: string;
  servicePort: number | string;
}

export interface NetworkPolicy {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  podSelector: Record<string, string>;
  policyTypes: string[];
  ingressRules: number;
  egressRules: number;
  createdAt: string;
}

// --- Gateway API Types ---
//
// Backend flatten functions live in internal/handler/resources.go
// (flattenGateway / flattenRouteResource / flattenGatewayClass /
// flattenReferenceGrant). The fields below mirror the JSON they emit.

export interface GatewayListener {
  name: string;
  protocol: string;
  port: number;
  hostname?: string;
}

export interface Gateway {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  gatewayClassName: string;
  listeners: GatewayListener[];
  listenerSummary: string[];
  listenerCount: number;
  addresses: string[];
  // Status of the Programmed/Accepted conditions, as raw "True"/"False"/"Unknown"
  // strings (empty when the status hasn't been published yet).
  programmed: string;
  accepted: string;
  createdAt: string;
}

export interface RouteParentRef {
  name: string;
  namespace?: string;
  sectionName?: string;
  kind?: string;
}

// Shared shape for HTTPRoute, GRPCRoute, TLSRoute, TCPRoute, UDPRoute. They
// differ in spec (HTTP rules vs raw L4) but agree on the metadata the UI
// needs: hostnames (when applicable), parent Gateways, and a rule count.
export interface GatewayRoute {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  hostnames: string[];
  parentRefs: RouteParentRef[];
  parentSummary: string[];
  ruleCount: number;
  createdAt: string;
}

export type HTTPRoute = GatewayRoute;
export type GRPCRoute = GatewayRoute;
export type TLSRoute = GatewayRoute;
export type TCPRoute = GatewayRoute;
export type UDPRoute = GatewayRoute;

export interface GatewayClass {
  name: string;
  clusterId: string;
  clusterName: string;
  controllerName: string;
  description: string;
  accepted: string;
  createdAt: string;
}

export interface ReferenceGrantFrom {
  group: string;
  kind: string;
  namespace: string;
}

export interface ReferenceGrantTo {
  group: string;
  kind: string;
  name: string;
}

export interface ReferenceGrant {
  name: string;
  namespace: string;
  clusterId: string;
  clusterName: string;
  from: ReferenceGrantFrom[];
  to: ReferenceGrantTo[];
  createdAt: string;
}
