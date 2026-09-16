import { useQuery } from "@tanstack/react-query";
import {
  listNetworkPolicyApplications,
  listNetworkPolicyTemplates,
  listComplianceBaselines,
  listComplianceBaselineApplications,
  getActiveComplianceBaseline,
  getComplianceBaselineDiff,
} from "@/lib/api/settings";
import { queryKeys } from "@/lib/query-keys";

export function useNetworkPolicyTemplates() {
  return useQuery({
    queryKey: queryKeys.networkPolicyTemplates,
    queryFn: ({ signal }) => listNetworkPolicyTemplates(signal),
  });
}

export function useNetworkPolicyApplications(clusterID: string) {
  return useQuery({
    queryKey: queryKeys.networkPolicyApplications(clusterID),
    queryFn: ({ signal }) => listNetworkPolicyApplications(clusterID, signal),
    enabled: !!clusterID,
  });
}

export function useComplianceBaselines() {
  return useQuery({
    queryKey: queryKeys.complianceBaselines,
    queryFn: async ({ signal }) => {
      const [baselines, history, { active }] = await Promise.all([
        listComplianceBaselines({ signal }),
        listComplianceBaselineApplications({ signal }),
        getActiveComplianceBaseline({ signal }),
      ]);
      return {
        baselines: baselines.map((baseline) => ({
          ...baseline,
          active: baseline.slug === active?.baselineSlug,
        })),
        history,
      };
    },
  });
}

export function useComplianceBaselineDiff(id: string) {
  return useQuery({
    queryKey: queryKeys.complianceBaselineDiff(id),
    queryFn: ({ signal }) => getComplianceBaselineDiff(id, { signal }),
  });
}
