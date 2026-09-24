import type { KubernetesManifest } from "@/components/resources/guided-resource-model";

/**
 * Server-managed `metadata` fields Kubernetes stamps on read and rejects (or
 * silently ignores) on create. A cloned object must not replay them.
 */
const STRIPPED_METADATA_KEYS = [
  "uid",
  "resourceVersion",
  "creationTimestamp",
  "generation",
  "managedFields",
  "ownerReferences",
  "selfLink",
  "deletionTimestamp",
  "deletionGracePeriodSeconds",
  "finalizers",
  "generateName",
] as const;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
}

/**
 * Project a fetched Kubernetes object into a create-ready clone: strips every
 * server-managed metadata field plus the whole `status` subtree, and renames
 * `metadata.name` to `<name>-copy` so the clone doesn't collide with its
 * source. Pure and synchronous — the row action wraps this around the fetch
 * (`k8sGetYaml`) and YAML (de)serialization.
 */
export function prepareCloneManifest(
  obj: KubernetesManifest,
): KubernetesManifest {
  const clone: KubernetesManifest = structuredClone(obj);
  delete clone.status;

  const metadata = { ...asRecord(clone.metadata) };
  for (const key of STRIPPED_METADATA_KEYS) {
    delete metadata[key];
  }
  if (typeof metadata.name === "string" && metadata.name) {
    metadata.name = `${metadata.name}-copy`;
  }
  clone.metadata = metadata;

  const annotations = asRecord(metadata.annotations);
  delete annotations["kubectl.kubernetes.io/last-applied-configuration"];
  if (Object.keys(annotations).length) metadata.annotations = annotations;
  else delete metadata.annotations;
  const spec = asRecord(clone.spec);
  if (clone.kind === "Service") {
    if (spec.clusterIP !== "None") {
      delete spec.clusterIP;
      delete spec.clusterIPs;
    }
    delete spec.healthCheckNodePort;
    if (Array.isArray(spec.ports))
      for (const port of spec.ports) delete asRecord(port).nodePort;
  }
  if (clone.kind === "PersistentVolumeClaim") {
    delete spec.volumeName;
    for (const key of Object.keys(annotations))
      if (
        key.startsWith("pv.kubernetes.io/") ||
        key.startsWith("volume.kubernetes.io/") ||
        key.startsWith("volume.beta.kubernetes.io/")
      )
        delete annotations[key];
  }

  return clone;
}
