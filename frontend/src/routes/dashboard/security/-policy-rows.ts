import { useQueries } from "@tanstack/react-query";
import { getPodSecurityTemplate } from "@/lib/api/security-policies";
import { queryKeys } from "@/lib/query-keys";
import type { ClusterSecurityPolicy } from "@/types";
import type { SecurityPolicyRow } from "./-policies-tab";

/** Resolve only templates referenced by the visible, bounded policy page. */
export function usePolicyRows(
  policies: ClusterSecurityPolicy[],
): SecurityPolicyRow[] {
  const ids = [...new Set(policies.map((row) => row.templateId))];
  const queries = useQueries({
    queries: ids.map((id) => ({
      queryKey: queryKeys.security.template(id),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getPodSecurityTemplate(id, signal),
      throwOnError: false as const,
    })),
  });
  return policies.map((policy) => {
    const query = queries[ids.indexOf(policy.templateId)];
    const template = query?.isError ? undefined : query?.data;
    return {
      ...policy,
      clusterName: policy.clusterId,
      templateName: template?.name || `Unavailable (${policy.templateId})`,
      enforceLevel: template?.enforceLevel || "unknown",
      auditLevel: template?.auditLevel || "unknown",
      warnLevel: template?.warnLevel || "unknown",
    };
  });
}
