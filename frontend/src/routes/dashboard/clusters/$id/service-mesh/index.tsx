import { createFileRoute } from "@tanstack/react-router";
/**
 * Cluster Service Mesh tab — Istio / Linkerd / Kuma / Cilium-mesh detection.
 *
 * Backend (sprint 071) populates cluster_service_mesh on a 5m worker cadence
 * plus on-demand via POST /service-mesh/detect/. This page just renders the
 * row and lets the operator click "Re-detect" for immediate feedback. Read-
 * only — install goes through the existing catalog deep-link with
 * ?tag=service-mesh.
 *
 * RBAC: clusters:read on the backend; the page assumes the caller is past
 * the auth gate (same as the snapshots page).
 */

import { Link as RouterLink } from "@tanstack/react-router";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { CheckCircle2, Loader2, RefreshCw, Server, Shield } from "lucide-react";

import { queryKeys } from "@/lib/query-keys";
import { useCluster } from "@/lib/hooks/clusters";
import { liveFallback } from "@/lib/live/status-store";
import {
  getServiceMeshInventory,
  getServiceMeshDetection,
  reDetectServiceMesh,
  validateServiceMeshPolicy,
  type ServiceMeshPolicyValidation,
} from "@/lib/api/cluster-service-mesh";
import { ActionButton } from "@/components/ui/action-button";
import { SkeletonText } from "@/components/ui/skeleton";
import { BARE_BUTTON } from "@/lib/bare-button";
import {
  HeroCard,
  HealthTile,
  InventoryPanel,
  PolicyValidationPanel,
} from "./-panels";

// ─── Page ───────────────────────────────────────────────────────────────────
function ClusterServiceMeshPage() {
  const params = Route.useParams();
  const clusterId = params.id;
  const queryClient = useQueryClient();
  const [policyYaml, setPolicyYaml] = useState("");
  const [validationResult, setValidationResult] = useState<
    ServiceMeshPolicyValidation | undefined
  >();

  const clusterQuery = useCluster(clusterId);
  const { data: cluster, isLoading: clusterLoading } = clusterQuery;
  const { data: detection, isLoading: detLoading } = useQuery({
    queryKey: queryKeys.clusterPages.serviceMeshDetection(clusterId),
    queryFn: () => getServiceMeshDetection(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(60_000),
    refetchIntervalInBackground: false,
  });
  const { data: inventory, isLoading: inventoryLoading } = useQuery({
    queryKey: queryKeys.clusterPages.serviceMeshInventory(clusterId),
    queryFn: () => getServiceMeshInventory(clusterId),
    enabled: !!clusterId,
    refetchInterval: liveFallback(60_000),
    refetchIntervalInBackground: false,
  });

  const reDetect = useMutation({
    mutationFn: () => reDetectServiceMesh(clusterId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.serviceMeshDetection(clusterId),
      });
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.serviceMeshInventory(clusterId),
      });
      toastSuccess("Service-mesh detection refreshed");
    },
    onError: (e: Error) => toastApiError("Re-detect failed", e),
  });
  const validatePolicy = useMutation({
    mutationFn: () =>
      validateServiceMeshPolicy(clusterId, { yaml: policyYaml }),
    onSuccess: (result) => {
      setValidationResult(result);
      if (result.valid) {
        toastSuccess("Policy validation passed");
      } else {
        toastWarning("Policy validation returned findings");
      }
    },
    onError: (e: Error) => toastApiError("Validation failed", e),
  });

  if (clusterLoading || clusterQuery.isError) {
    return (
      <QueryStates
        query={clusterQuery}
        permission="clusters:read"
        notFound={
          <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
            <Server className="h-8 w-8 mb-3" />
            <p>Cluster not found</p>
          </div>
        }
      >
        {() => null}
      </QueryStates>
    );
  }
  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  return (
    <PageShell>
      {/* Header */}
      <PageHeader
        title="Service mesh"
        description={`Detect and monitor the service mesh installed on ${cluster.displayName}.`}
        actions={
          <ActionButton
            {...BARE_BUTTON}
            onClick={() => reDetect.mutate()}
            disabled={reDetect.isPending}
            className="inline-flex items-center gap-1.5 h-(--control-h) px-3 rounded-sm text-sm font-medium
              border border-border text-foreground hover:bg-accent transition-colors
              disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {reDetect.isPending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <RefreshCw className="h-3.5 w-3.5" />
            )}
            Re-detect
          </ActionButton>
        }
      />

      {/* Hero card */}
      {detLoading ? (
        <div
          className="rounded-lg border border-border bg-card p-6 h-32"
          aria-busy="true"
        >
          <span className="sr-only">Loading service mesh detection…</span>
          <SkeletonText lines={3} />
        </div>
      ) : detection ? (
        <HeroCard detection={detection} clusterId={clusterId} />
      ) : null}

      {/* Health 4-grid (Istio counts when Istio, Linkerd counts when Linkerd) */}
      {detection && detection.detectedMesh === "istio" && (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <HealthTile label="Gateways" value={detection.gatewayCount} />
          <HealthTile
            label="VirtualServices"
            value={detection.virtualServiceCount}
          />
          <HealthTile
            label="DestinationRules"
            value={detection.destinationRuleCount}
          />
          <HealthTile
            label="mTLS coverage"
            value={detection.mtlsCoveragePct}
            suffix="%"
            hint={
              detection.peerAuthenticationCount + " PeerAuthentication rules"
            }
          />
        </div>
      )}
      {detection && detection.detectedMesh === "linkerd" && (
        <div className="grid grid-cols-2 lg:grid-cols-3 gap-4">
          <HealthTile
            label="ServiceProfiles"
            value={detection.serviceProfileCount}
          />
          <HealthTile label="Servers" value={detection.serverAuthCount} />
          <HealthTile
            label="mTLS coverage"
            value={detection.mtlsCoveragePct}
            suffix="%"
            hint="Linkerd Server-level proxy auth"
          />
        </div>
      )}

      {/* mTLS breakdown link */}
      {detection &&
        detection.detectedMesh !== "none" &&
        detection.detectedMesh !== "unknown" && (
          <div className="rounded-lg border border-border bg-card p-4 flex items-center justify-between">
            <div className="flex items-start gap-3">
              <Shield className="h-5 w-5 text-status-success shrink-0 mt-0.5" />
              <div>
                <p className="text-sm font-medium text-foreground">
                  mTLS posture
                </p>
                <p className="text-xs text-muted-foreground mt-0.5">
                  {detection.mtlsCoveragePct}% of user namespaces covered.
                </p>
              </div>
            </div>
            <RouterLink
              to="/dashboard/clusters/$id/service-mesh/mtls"
              params={{ id: clusterId }}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-sm text-xs font-medium
              border border-border text-foreground hover:bg-accent transition-colors"
            >
              View breakdown
            </RouterLink>
          </div>
        )}

      <InventoryPanel inventory={inventory} loading={inventoryLoading} />

      {detection &&
        detection.detectedMesh !== "none" &&
        detection.detectedMesh !== "unknown" && (
          <PolicyValidationPanel
            value={policyYaml}
            onChange={(value) => {
              setPolicyYaml(value);
              setValidationResult(undefined);
            }}
            result={validationResult}
            validating={validatePolicy.isPending}
            onValidate={() => validatePolicy.mutate()}
          />
        )}

      {/* "no mesh" install CTA already lives in HeroCard; surface a hint here too */}
      {detection && detection.detectedMesh === "none" && (
        <div className="rounded-lg border border-border bg-card p-6 flex items-start gap-3">
          <CheckCircle2 className="h-5 w-5 text-muted-foreground shrink-0 mt-0.5" />
          <div className="flex-1">
            <p className="text-sm font-medium text-foreground">
              No service mesh installed
            </p>
            <p className="text-xs text-muted-foreground mt-1">
              Use the catalog to install Istio, Linkerd, Kuma, or Cilium-mesh.
              The "Install a mesh" button above deep-links to the catalog
              filtered to service-mesh charts.
            </p>
          </div>
        </div>
      )}
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/service-mesh/")({
  component: ClusterServiceMeshPage,
});
