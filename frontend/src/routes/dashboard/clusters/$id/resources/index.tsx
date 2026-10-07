import { createFileRoute } from "@tanstack/react-router";
import { PageHeader, PageShell } from "@/components/ui/page";
/**
 * Cluster Resources tab — sprint 069 CRD-mirror v2 read-only view.
 *
 * Renders five expandable cards over the mirrored_* tables:
 *   - Ingress classes (with the "default" badge)
 *   - Gateway classes (with the Accepted condition)
 *   - Network policies (full mirror, managed-by-astronomer badge where applicable)
 *   - Resource quotas (used/hard progress bars)
 *   - Limit ranges (per-type defaults / requests)
 *
 * Data comes from the management plane's mirrored_* tables — no
 * round-trip through kubectl on every render. The per-cluster CRD
 * agent keeps the rows fresh; the server's periodic prune (every 30m)
 * drops rows whose agent stopped reporting, so the UI never has to
 * second-guess whether a row is still in the cluster.
 */

import { useQuery } from "@tanstack/react-query";
import { QueryStates } from "@/components/ui/query-states";
import { Layers, Network, Shield, Slash, SquareStack } from "lucide-react";

import {
  listMirroredGatewayClasses,
  listMirroredIngressClasses,
  listMirroredLimitRanges,
  listMirroredNetworkPolicies,
  listMirroredResourceQuotas,
} from "@/lib/api/cluster-resource-inventory";
import { queryKeys } from "@/lib/query-keys";
import { Section } from "./-shared";
import {
  IngressClassesTable,
  GatewayClassesTable,
  NetworkPoliciesTable,
  ResourceQuotasView,
  LimitRangesTable,
} from "./-tables";
// ---------------------------------------------------------------------

function ClusterResourcesPage() {
  const { id } = Route.useParams();

  const ingressClassesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredIngressClasses(id),
    queryFn: () => listMirroredIngressClasses(id),
  });
  const gatewayClassesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredGatewayClasses(id),
    queryFn: () => listMirroredGatewayClasses(id),
  });
  const networkPoliciesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredNetworkPolicies(id),
    queryFn: () => listMirroredNetworkPolicies(id),
  });
  const resourceQuotasQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredResourceQuotas(id),
    queryFn: () => listMirroredResourceQuotas(id),
  });
  const limitRangesQ = useQuery({
    queryKey: queryKeys.clusterPages.mirroredLimitRanges(id),
    queryFn: () => listMirroredLimitRanges(id),
  });

  return (
    <PageShell>
      <PageHeader
        title="Cluster resources"
        description="A read-only view of the policy / routing / quota objects installed in this cluster. Data is mirrored from the cluster agent every ~10 minutes; rows you delete in the cluster disappear here within roughly an hour."
        className="mb-4"
      />

      {ingressClassesQ.isError && (
        <QueryStates query={ingressClassesQ} permission="clusters:read">
          {() => null}
        </QueryStates>
      )}
      {gatewayClassesQ.isError && (
        <QueryStates query={gatewayClassesQ} permission="clusters:read">
          {() => null}
        </QueryStates>
      )}
      {networkPoliciesQ.isError && (
        <QueryStates query={networkPoliciesQ} permission="clusters:read">
          {() => null}
        </QueryStates>
      )}
      {resourceQuotasQ.isError && (
        <QueryStates query={resourceQuotasQ} permission="clusters:read">
          {() => null}
        </QueryStates>
      )}
      {limitRangesQ.isError && (
        <QueryStates query={limitRangesQ} permission="clusters:read">
          {() => null}
        </QueryStates>
      )}

      <Section
        title="Ingress classes"
        icon={<Layers className="h-4 w-4" />}
        count={ingressClassesQ.data?.length ?? 0}
      >
        <IngressClassesTable rows={ingressClassesQ.data ?? []} />
      </Section>

      <Section
        title="Gateway classes"
        icon={<Network className="h-4 w-4" />}
        count={gatewayClassesQ.data?.length ?? 0}
      >
        <GatewayClassesTable rows={gatewayClassesQ.data ?? []} />
      </Section>

      <Section
        title="Network policies"
        icon={<Shield className="h-4 w-4" />}
        count={networkPoliciesQ.data?.length ?? 0}
      >
        <NetworkPoliciesTable rows={networkPoliciesQ.data ?? []} />
      </Section>

      <Section
        title="Resource quotas"
        icon={<SquareStack className="h-4 w-4" />}
        count={resourceQuotasQ.data?.length ?? 0}
      >
        <ResourceQuotasView rows={resourceQuotasQ.data ?? []} />
      </Section>

      <Section
        title="Limit ranges"
        icon={<Slash className="h-4 w-4" />}
        count={limitRangesQ.data?.length ?? 0}
      >
        <LimitRangesTable rows={limitRangesQ.data ?? []} />
      </Section>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/resources/")({
  component: ClusterResourcesPage,
});
