import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";
import {
  getImageVulnReportHistory,
  getImageVulnSummary,
  getImageVulnHistory,
  getImageVulnDiff,
  getImageVulnProgress,
  getImageVulnRescanOperation,
  listVulnerableImages,
  triggerImageVulnRescan,
  type ImageVulnReport,
} from "@/lib/api/cluster-vulnerabilities";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";

export function useImageScanQueries({
  clusterId,
  scansEnabled,
  namespace,
  openReport,
}: {
  clusterId: string;
  scansEnabled: boolean;
  namespace: string;
  openReport: ImageVulnReport | null;
}) {
  const queryClient = useQueryClient();
  // Sprint 081: after a manual "Trigger rescan" click we accelerate
  // the progress poll for ~60s and show a "dispatched" banner state.
  // Trivy completes scans in <30s on small clusters, so a slow idle
  // poll would miss the scanning state entirely; this stretches the
  // user-visible feedback window so the click always produces a
  // legible response.
  const [lastRescanAt, setLastRescanAt] = useState<number | null>(null);
  const summary = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnSummary(clusterId),
    queryFn: () => getImageVulnSummary(clusterId),
    enabled: scansEnabled,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });

  const images = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnImages(clusterId, namespace),
    queryFn: () =>
      listVulnerableImages(clusterId, {
        namespace: namespace || undefined,
        limit: 20,
      }),
    enabled: scansEnabled,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });

  // Per-image scan history for the drawer. Only fires when a row is
  // open; refreshes on the same 30s cadence as the cluster aggregates
  // so the drawer stays in sync if a new snapshot lands while open.
  const reportHistory = useQuery({
    queryKey: openReport
      ? queryKeys.clusterPages.imageVulnReportHistory(clusterId, openReport.id)
      : ["noop-rh"],
    queryFn: () =>
      openReport
        ? getImageVulnReportHistory(clusterId, openReport.id, { limit: 50 })
        : Promise.resolve(null),
    enabled: scansEnabled && !!openReport,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });

  const rescan = useOperationMutation({
    keyPrefix: "vulnerability-rescan",
    submit: (_: void, context) => triggerImageVulnRescan(clusterId, context),
    read: getImageVulnRescanOperation,
    mutation: {
      onSuccess: () => {
        toastSuccess(
          "Vulnerability rescan completed; fresh reports will appear shortly",
        );
        // Mark the click time so the progress banner enters
        // "dispatched, waiting for jobs" mode and the progress query
        // polls at 1.5s for the next 60s — long enough to catch the
        // 5-15s scan window most clusters produce.
        setLastRescanAt(Date.now());
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusterPages.imageVulnSummary(clusterId),
        });
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusterPages.imageVulnHistory(clusterId, 24 * 30),
        });
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusterPages.imageVulnDiff(clusterId, 24),
        });
        queryClient.invalidateQueries({
          queryKey: queryKeys.clusterPages.imageVulnProgress(clusterId),
        });
      },
      onError: (err) => toastApiError("Rescan failed", err),
    },
  });

  // Sprint 081: scan history sparkline (last 30 days) + diff vs 24h
  // ago. Both refresh on the same 30s cadence as the summary so the
  // page stays in sync as new Trivy reports flow in.
  const history = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnHistory(clusterId, 24 * 30),
    queryFn: () =>
      getImageVulnHistory(clusterId, { sinceHours: 24 * 30, limit: 200 }),
    enabled: scansEnabled,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });
  const diff = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnDiff(clusterId, 24),
    queryFn: () => getImageVulnDiff(clusterId, 24),
    enabled: scansEnabled,
    refetchInterval: liveFallback(30_000),
    refetchIntervalInBackground: false,
  });

  // Live scan-in-progress polling.
  //   • scanning detected            → 3s   (catch progress as it changes)
  //   • rescan clicked within 60s    → 1.5s (most scans finish < polling)
  //   • otherwise                    → 30s  (don't hammer the tunnel)
  const progress = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnProgress(clusterId),
    queryFn: () => getImageVulnProgress(clusterId),
    enabled: scansEnabled,
    refetchInterval: (query) => {
      if (query.state.data?.scanning) return 3_000;
      if (lastRescanAt && Date.now() - lastRescanAt < 60_000) return 1_500;
      // Idle: `image_scan.changed` events refresh the aggregates while the
      // stream is open; poll only as the stream-down fallback.
      return liveFallback(30_000)();
    },
    refetchIntervalInBackground: false,
  });

  return {
    summary,
    images,
    reportHistory,
    rescan,
    history,
    diff,
    progress,
    lastRescanAt,
  };
}
