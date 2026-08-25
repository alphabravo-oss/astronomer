import api from "@/lib/api/transport";
import { postClustersByIdGenerateDirectKubeconfig } from "@/lib/api/generated/client";

export async function downloadProxyKubeconfig(clusterId: string) {
  const res = await api.post(
    `/clusters/${clusterId}/generate-kubeconfig/`,
    null,
    { responseType: "blob" },
  );
  return res.data;
}

export async function downloadDirectKubeconfig(clusterId: string) {
  const yaml = await postClustersByIdGenerateDirectKubeconfig({
    path: { id: clusterId },
  });
  return new Blob([yaml], { type: "application/x-yaml;charset=utf-8" });
}

/** Send any supported method through the agent-backed Kubernetes API proxy. */
export async function k8sProxy(
  clusterId: string,
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  k8sPath: string,
  body?: unknown,
  headers?: Record<string, string>,
) {
  const url = `/clusters/${clusterId}/k8s/${k8sPath}`;
  const res = await api.request({
    url,
    method,
    data: body,
    headers,
    timeout: 60000,
  });
  return res.data;
}

export async function k8sGet(clusterId: string, path: string) {
  return k8sProxy(clusterId, "GET", path);
}

export async function k8sCreate(
  clusterId: string,
  path: string,
  body: unknown,
) {
  return k8sProxy(clusterId, "POST", path, body);
}

export async function k8sUpdate(
  clusterId: string,
  path: string,
  body: unknown,
) {
  return k8sProxy(clusterId, "PUT", path, body);
}

export async function k8sDryRunYaml(
  clusterId: string,
  path: string,
  yamlStr: string,
) {
  const yaml = await import("js-yaml");
  const body = yaml.load(yamlStr);
  const dryRunPath = appendK8sQuery(path, {
    dryRun: "All",
    fieldManager: "astronomer",
    fieldValidation: "Strict",
  });
  return k8sProxy(clusterId, "PATCH", dryRunPath, body, {
    "Content-Type": "application/apply-patch+yaml",
  });
}

export async function k8sPatch(
  clusterId: string,
  path: string,
  body: unknown,
  patchType: "strategic-merge" | "merge" | "json" = "strategic-merge",
) {
  const contentTypeMap = {
    "strategic-merge": "application/strategic-merge-patch+json",
    merge: "application/merge-patch+json",
    json: "application/json-patch+json",
  };
  return k8sProxy(clusterId, "PATCH", path, body, {
    "Content-Type": contentTypeMap[patchType],
  });
}

export async function k8sDelete(clusterId: string, path: string) {
  return k8sProxy(clusterId, "DELETE", path);
}

function appendK8sQuery(path: string, params: Record<string, string>): string {
  const [base, rawQuery = ""] = path.split("?", 2);
  const search = new URLSearchParams(rawQuery);
  for (const [key, value] of Object.entries(params)) search.set(key, value);
  const query = search.toString();
  return query ? `${base}?${query}` : base;
}

export async function k8sGetYaml(
  clusterId: string,
  path: string,
): Promise<string> {
  const data = await k8sGet(clusterId, path);
  const yaml = await import("js-yaml");
  if (data?.metadata?.managedFields) delete data.metadata.managedFields;
  return yaml.dump(data, { lineWidth: -1, noRefs: true });
}

export async function k8sApplyYaml(
  clusterId: string,
  path: string,
  yamlStr: string,
  force = false,
) {
  const yaml = await import("js-yaml");
  const body = yaml.load(yamlStr);
  const applyPath = appendK8sQuery(path, {
    fieldManager: "astronomer",
    ...(force ? { force: "true" } : {}),
  });
  return k8sProxy(clusterId, "PATCH", applyPath, body, {
    "Content-Type": "application/apply-patch+yaml",
  });
}
