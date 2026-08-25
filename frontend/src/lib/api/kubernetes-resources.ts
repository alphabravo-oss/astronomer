import api from "@/lib/api/transport";
import type { APIResponse } from "@/types";

// Storage

export async function getPersistentVolumes(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").PersistentVolume[]>>(
    `/clusters/${clusterId}/resources/persistentvolumes`,
  );
  return res.data.data;
}

export async function getPersistentVolumeClaims(clusterId: string) {
  const res = await api.get<
    APIResponse<import("@/types").PersistentVolumeClaim[]>
  >(`/clusters/${clusterId}/resources/persistentvolumeclaims`);
  return res.data.data;
}

export async function getStorageClasses(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").StorageClass[]>>(
    `/clusters/${clusterId}/resources/storageclasses`,
  );
  return res.data.data;
}

export async function createPersistentVolumeClaim(
  clusterId: string,
  data: Partial<import("@/types").PersistentVolumeClaim>,
) {
  const res = await api.post<
    APIResponse<import("@/types").PersistentVolumeClaim>
  >(`/clusters/${clusterId}/resources/persistentvolumeclaims`, data);
  return res.data.data;
}

export async function deletePersistentVolumeClaim(
  clusterId: string,
  namespace: string,
  name: string,
) {
  await api.delete(
    `/clusters/${clusterId}/resources/persistentvolumeclaims/${namespace}/${name}`,
  );
}

export async function deletePersistentVolume(clusterId: string, name: string) {
  await api.delete(
    `/clusters/${clusterId}/resources/persistentvolumes/${name}`,
  );
}

// Core networking

export async function getServices(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").K8sService[]>>(
    `/clusters/${clusterId}/resources/services`,
  );
  return res.data.data;
}

export async function createService(
  clusterId: string,
  data: Partial<import("@/types").K8sService>,
) {
  const res = await api.post<APIResponse<import("@/types").K8sService>>(
    `/clusters/${clusterId}/resources/services`,
    data,
  );
  return res.data.data;
}

export async function deleteService(
  clusterId: string,
  namespace: string,
  name: string,
) {
  await api.delete(
    `/clusters/${clusterId}/resources/services/${namespace}/${name}`,
  );
}

export async function getIngresses(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").Ingress[]>>(
    `/clusters/${clusterId}/resources/ingresses`,
  );
  return res.data.data;
}

export async function createIngress(
  clusterId: string,
  data: Partial<import("@/types").Ingress>,
) {
  const res = await api.post<APIResponse<import("@/types").Ingress>>(
    `/clusters/${clusterId}/resources/ingresses`,
    data,
  );
  return res.data.data;
}

export async function deleteIngress(
  clusterId: string,
  namespace: string,
  name: string,
) {
  await api.delete(
    `/clusters/${clusterId}/resources/ingresses/${namespace}/${name}`,
  );
}

export async function getNetworkPolicies(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").NetworkPolicy[]>>(
    `/clusters/${clusterId}/resources/networkpolicies`,
  );
  return res.data.data;
}

export async function createNetworkPolicy(
  clusterId: string,
  data: Partial<import("@/types").NetworkPolicy>,
) {
  const res = await api.post<APIResponse<import("@/types").NetworkPolicy>>(
    `/clusters/${clusterId}/resources/networkpolicies`,
    data,
  );
  return res.data.data;
}

export async function deleteNetworkPolicy(
  clusterId: string,
  namespace: string,
  name: string,
) {
  await api.delete(
    `/clusters/${clusterId}/resources/networkpolicies/${namespace}/${name}`,
  );
}

// Gateway API list endpoints return UI-ready rows from the structured backend
// resource handlers. Mutation callers use the generic K8s proxy helpers.

export async function getGateways(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").Gateway[]>>(
    `/clusters/${clusterId}/resources/gateways`,
  );
  return res.data.data;
}

export async function getHTTPRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").HTTPRoute[]>>(
    `/clusters/${clusterId}/resources/httproutes`,
  );
  return res.data.data;
}

export async function getGatewayClasses(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").GatewayClass[]>>(
    `/clusters/${clusterId}/resources/gatewayclasses`,
  );
  return res.data.data;
}

export async function getGRPCRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").GRPCRoute[]>>(
    `/clusters/${clusterId}/resources/grpcroutes`,
  );
  return res.data.data;
}

export async function getTLSRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").TLSRoute[]>>(
    `/clusters/${clusterId}/resources/tlsroutes`,
  );
  return res.data.data;
}

export async function getTCPRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").TCPRoute[]>>(
    `/clusters/${clusterId}/resources/tcproutes`,
  );
  return res.data.data;
}

export async function getUDPRoutes(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").UDPRoute[]>>(
    `/clusters/${clusterId}/resources/udproutes`,
  );
  return res.data.data;
}

export async function getReferenceGrants(clusterId: string) {
  const res = await api.get<APIResponse<import("@/types").ReferenceGrant[]>>(
    `/clusters/${clusterId}/resources/referencegrants`,
  );
  return res.data.data;
}
