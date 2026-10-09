import type { ClusterRegistry } from "@/lib/api/cluster-registries";
export const PASSWORD_SENTINEL = "<set>";
export function registryDefaults(existing?: ClusterRegistry) {
  const isEdit = !!existing;
  return {
    registryUrl: existing?.registryUrl || "",
    username: existing?.username || "",
    // Edit seeds the sentinel — the password is only sent when the user
    // actually types a new one (round-trip variant, unchanged).
    password: isEdit ? PASSWORD_SENTINEL : "",
    selectedNs: (existing?.namespaces || []) as string[],
    secretName: existing?.secretName || "",
    injectDefaultSa: existing?.injectDefaultSa ?? false,
  };
}
