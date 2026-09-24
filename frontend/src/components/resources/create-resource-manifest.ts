import type { ClusterDiscovery } from "@/components/layout/cluster-discovery-model";
import type { ResourceSchemaView } from "@/lib/api/resources";
import type { K8sCreateBatchItem } from "@/lib/hooks/kubernetes-proxy";
import type { KubernetesManifest } from "./guided-resource-form";

// Match the full GVK, never kind alone: a CRD can share a built-in kind name.
const BUILTIN_PLURALS: Record<string, string> = {
  "apps/v1/Deployment": "deployments",
  "apps/v1/StatefulSet": "statefulsets",
  "apps/v1/DaemonSet": "daemonsets",
  "batch/v1/Job": "jobs",
  "batch/v1/CronJob": "cronjobs",
  "v1/Service": "services",
  "networking.k8s.io/v1/Ingress": "ingresses",
  "networking.k8s.io/v1/NetworkPolicy": "networkpolicies",
  "v1/ConfigMap": "configmaps",
  "v1/Secret": "secrets",
  "v1/Namespace": "namespaces",
  "v1/PersistentVolumeClaim": "persistentvolumeclaims",
  "policy/v1/PodDisruptionBudget": "poddisruptionbudgets",
  "autoscaling/v1/HorizontalPodAutoscaler": "horizontalpodautoscalers",
  "autoscaling/v2/HorizontalPodAutoscaler": "horizontalpodautoscalers",
  "v1/ServiceAccount": "serviceaccounts",
  "rbac.authorization.k8s.io/v1/Role": "roles",
  "rbac.authorization.k8s.io/v1/RoleBinding": "rolebindings",
};

export function normalizeManifestDocuments(
  documents: unknown[],
): KubernetesManifest[] {
  const manifests = documents.filter((document) => document != null);
  if (manifests.length === 0) {
    throw new Error("YAML must contain at least one Kubernetes object.");
  }
  if (manifests.length > 50) {
    throw new Error(
      `YAML contains ${manifests.length} objects; the maximum is 50.`,
    );
  }
  return manifests.map((document, index) => {
    if (typeof document !== "object" || Array.isArray(document)) {
      throw new Error(
        `YAML document ${index + 1} must be a Kubernetes object.`,
      );
    }
    return document as KubernetesManifest;
  });
}

export function createPathForManifest(
  body: KubernetesManifest,
  schema?: ResourceSchemaView,
  explicitPath?: string,
  discovery?: ClusterDiscovery,
): string | undefined {
  if (explicitPath) return explicitPath.replace(/^\//, "");
  const metadata = body.metadata as { namespace?: string } | null | undefined;
  const namespace = encodeURIComponent(metadata?.namespace || "default");
  if (schema?.resource.apiBase && schema.resource.plural) {
    const base = schema.resource.apiBase.replace(/^\//, "");
    return schema.resource.namespaced
      ? `${base}/namespaces/${namespace}/${schema.resource.plural}`
      : `${base}/${schema.resource.plural}`;
  }
  const apiVersion = typeof body.apiVersion === "string" ? body.apiVersion : "";
  const kind = typeof body.kind === "string" ? body.kind : "";
  const builtin = BUILTIN_PLURALS[`${apiVersion}/${kind}`];
  if (builtin) {
    const base = apiVersion.includes("/")
      ? `apis/${apiVersion}`
      : `api/${apiVersion}`;
    return kind === "Namespace"
      ? `${base}/${builtin}`
      : `${base}/namespaces/${namespace}/${builtin}`;
  }
  if (!discovery || discovery.isLoading || discovery.isError) return undefined;
  const [group, version, extra] = apiVersion.split("/");
  if (!group || !version || extra !== undefined) return undefined;
  const resource = discovery.crdsByGroup
    .get(group)
    ?.find(
      (type) => type.kind === kind && type.servedVersions.includes(version),
    );
  if (!resource) return undefined;
  const base = `apis/${group}/${version}`;
  return resource.namespaced
    ? `${base}/namespaces/${namespace}/${resource.plural}`
    : `${base}/${resource.plural}`;
}

/** Resolve every document before issuing any writes (including mixed imports). */
export function createManifestBatch(
  documents: unknown[],
  schema: ResourceSchemaView | undefined,
  explicitPath: string | undefined,
  discovery: ClusterDiscovery,
): K8sCreateBatchItem[] {
  const bodies = normalizeManifestDocuments(documents);
  return bodies.map((body, index) => {
    const metadata = body.metadata as { name?: string } | null | undefined;
    const kind = typeof body.kind === "string" ? body.kind : "Object";
    const label = metadata?.name
      ? `${kind}/${metadata.name}`
      : `${kind} #${index + 1}`;
    // Schema/route describe only the dialog's primary resource, not a batch.
    const single = bodies.length === 1;
    const path = createPathForManifest(
      body,
      single ? schema : undefined,
      single ? explicitPath : undefined,
      discovery,
    );
    if (!path) {
      const reason = discovery.isLoading
        ? "Resource discovery is still loading. Try again when it finishes."
        : discovery.isError
          ? "Resource discovery is unavailable or access was denied."
          : "Check the API group, served version, and kind against the installed CRDs.";
      throw new Error(
        `Cannot determine the API endpoint for ${label}. ${reason}`,
      );
    }
    return { id: String(index), path, body, label };
  });
}
