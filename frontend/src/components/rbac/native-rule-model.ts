import type { CreateNativeRuleRequest } from "@/lib/api/native-rbac";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const DENIED_GROUPS = new Set([
  "rbac.authorization.k8s.io",
  "admissionregistration.k8s.io",
  "apiregistration.k8s.io",
  "apiextensions.k8s.io",
]);
export function nativeRuleError(
  value: CreateNativeRuleRequest,
): string | undefined {
  if (!UUID.test(value.userId)) return "Enter a user UUID.";
  if (value.clusterId && !UUID.test(value.clusterId))
    return "Select a valid cluster.";
  if (
    value.namespace &&
    (!/^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$/.test(value.namespace) ||
      value.namespace.length > 63)
  )
    return "Namespace must be a DNS label.";
  if (DENIED_GROUPS.has((value.apiGroup ?? "").toLowerCase()))
    return "Native grants cannot target privilege-escalation API groups.";
  if (!value.resource.trim()) return "Enter a plural resource name.";
  if (!value.verbs.length) return "Select at least one verb.";
  return undefined;
}
