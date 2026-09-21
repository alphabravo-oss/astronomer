import { useMemo, useState } from "react";
import {
  Activity,
  Boxes,
  Braces,
  Gauge,
  Network,
  Server,
  ShieldCheck,
  TriangleAlert,
} from "lucide-react";

import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  useClusterEvents,
  useClusterNamespaces,
  useClusterPods,
} from "@/lib/hooks/clusters";
import { useGenericResources } from "@/lib/hooks/kubernetes-proxy";
import {
  useIngresses,
  useNetworkPolicies,
  usePersistentVolumeClaims,
  useServices,
} from "@/lib/hooks/kubernetes-resources";
import { useWorkloads } from "@/lib/hooks/workloads";
import { Link } from "@tanstack/react-router";
import { cn, formatBytes, formatCPU, formatRelativeTime } from "@/lib/utils";
import type { GenericK8sResource } from "@/types";

type TabId =
  | "overview"
  | "workloads"
  | "pods"
  | "networking"
  | "storage"
  | "configuration"
  | "events"
  | "security"
  | "usage";

interface NamespaceResourceRow {
  id: string;
  kind: string;
  name: string;
  status: string;
  detail: string;
  age: string;
  createdAt: string;
  href?: string;
}

const tabs: Array<{ id: TabId; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "workloads", label: "Workloads" },
  { id: "pods", label: "Pods" },
  { id: "networking", label: "Networking" },
  { id: "storage", label: "Storage" },
  { id: "configuration", label: "Configuration" },
  { id: "events", label: "Events" },
  { id: "security", label: "Security" },
  { id: "usage", label: "Resource usage" },
];

const resourceColumns: Column<NamespaceResourceRow>[] = [
  { key: "kind", header: "Kind", accessor: (row) => row.kind },
  {
    key: "name",
    header: "Name",
    accessor: (row) =>
      row.href ? (
        <Link
          className="font-mono text-xs font-medium hover:underline"
          to={row.href}
        >
          {row.name}
        </Link>
      ) : (
        <span className="font-mono text-xs font-medium">{row.name}</span>
      ),
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  { key: "detail", header: "Details", accessor: (row) => row.detail },
  {
    key: "age",
    header: "Age",
    accessor: (row) => row.age,
    sortAccessor: (row) => Date.parse(row.createdAt) || 0,
  },
];

export const buildGenericNamespaceRows = (
  clusterId: string,
  namespace: string,
  resourceType: string,
  kind: string,
  items: GenericK8sResource[] | undefined,
): NamespaceResourceRow[] =>
  (items ?? [])
    .filter((item) => item.namespace === namespace)
    .map((item) => ({
      id: `${kind}/${item.name}`,
      kind,
      name: item.name,
      status: item.status || "Active",
      detail: "",
      createdAt: item.createdAt || "",
      age: item.createdAt ? formatRelativeTime(item.createdAt) : "—",
      href: `/dashboard/clusters/${clusterId}/${resourceType}/${namespace}/${item.name}`,
    }));

export function NamespaceDetailPage({
  clusterId,
  namespace,
}: {
  clusterId: string;
  namespace: string;
}) {
  const [tab, setTab] = useState<TabId>("overview");
  const namespaces = useClusterNamespaces(clusterId);
  const workloads = useWorkloads(clusterId, { namespace, pageSize: 500 });
  const pods = useClusterPods(clusterId, { namespace });
  const services = useServices(clusterId);
  const ingresses = useIngresses(clusterId);
  const policies = useNetworkPolicies(clusterId);
  const pvcs = usePersistentVolumeClaims(clusterId);
  const events = useClusterEvents(clusterId, { limit: 500 });
  const configMaps = useGenericResources(clusterId, "configmaps");
  const secrets = useGenericResources(clusterId, "secrets");
  const quotas = useGenericResources(clusterId, "resourcequotas");
  const limits = useGenericResources(clusterId, "limitranges");
  const serviceAccounts = useGenericResources(clusterId, "serviceaccounts");
  const roles = useGenericResources(clusterId, "k8s-roles");
  const roleBindings = useGenericResources(clusterId, "k8s-rolebindings");

  const ns = namespaces.data?.find((item) => item.name === namespace);
  const namespaceEvents = (events.data ?? []).filter(
    (event) => event.involvedObject.namespace === namespace,
  );
  const warningEvents = namespaceEvents.filter(
    (event) => event.type === "Warning",
  );
  const namespacePods = useMemo(() => pods.data?.data ?? [], [pods.data]);
  const unhealthyPods = namespacePods.filter(
    (pod) => pod.phase !== "Running" && pod.phase !== "Succeeded",
  );
  const restarts = namespacePods.reduce((sum, pod) => sum + pod.restarts, 0);

  const rows = useMemo<
    Record<Exclude<TabId, "overview" | "usage">, NamespaceResourceRow[]>
  >(
    () => ({
      workloads: (workloads.data?.data ?? []).map((item) => ({
        id: `${item.kind}/${item.name}`,
        kind: item.kind,
        name: item.name,
        status: item.status,
        detail: item.ready,
        createdAt: item.createdAt,
        age: item.age,
        href: `/dashboard/clusters/${clusterId}/workloads/${item.kind.toLowerCase()}s/${namespace}/${item.name}`,
      })),
      pods: namespacePods.map((pod) => ({
        id: `Pod/${pod.name}`,
        kind: "Pod",
        name: pod.name,
        status: pod.status,
        detail: `${pod.ready} ready · ${pod.restarts} restarts · ${pod.node || "unscheduled"}`,
        createdAt: pod.createdAt,
        age: pod.age,
        href: `/dashboard/clusters/${clusterId}/pods/${namespace}/${pod.name}`,
      })),
      networking: [
        ...(services.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `Service/${item.name}`,
            kind: "Service",
            name: item.name,
            status: "Active",
            detail: `${item.type} · ${item.clusterIP || "no cluster IP"}`,
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/services/${namespace}/${item.name}`,
          })),
        ...(ingresses.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `Ingress/${item.name}`,
            kind: "Ingress",
            name: item.name,
            status: "Active",
            detail: item.hosts.join(", ") || "No hosts",
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/ingresses/${namespace}/${item.name}`,
          })),
        ...(policies.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `NetworkPolicy/${item.name}`,
            kind: "NetworkPolicy",
            name: item.name,
            status: "Active",
            detail: item.policyTypes.join(" + "),
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/networkpolicies/${namespace}/${item.name}`,
          })),
      ],
      storage: (pvcs.data ?? [])
        .filter((item) => item.namespace === namespace)
        .map((item) => ({
          id: `PVC/${item.name}`,
          kind: "PersistentVolumeClaim",
          name: item.name,
          status: item.status,
          detail: `${item.capacity || "unallocated"} · ${item.storageClass || "default class"}`,
          createdAt: item.createdAt,
          age: formatRelativeTime(item.createdAt),
          href: `/dashboard/clusters/${clusterId}/persistentvolumeclaims/${namespace}/${item.name}`,
        })),
      configuration: [
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "configmaps",
          "ConfigMap",
          configMaps.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "secrets",
          "Secret",
          secrets.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "resourcequotas",
          "ResourceQuota",
          quotas.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "limitranges",
          "LimitRange",
          limits.data?.data,
        ),
      ],
      events: namespaceEvents.map((event) => ({
        id: event.id,
        kind: event.involvedObject.kind,
        name: event.involvedObject.name,
        status: event.type,
        detail: `${event.reason}: ${event.message}`,
        createdAt: event.lastTimestamp,
        age: formatRelativeTime(event.lastTimestamp),
      })),
      security: [
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "serviceaccounts",
          "ServiceAccount",
          serviceAccounts.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "k8s-roles",
          "Role",
          roles.data?.data,
        ),
        ...buildGenericNamespaceRows(
          clusterId,
          namespace,
          "k8s-rolebindings",
          "RoleBinding",
          roleBindings.data?.data,
        ),
        ...(policies.data ?? [])
          .filter((item) => item.namespace === namespace)
          .map((item) => ({
            id: `NetworkPolicy/${item.name}`,
            kind: "NetworkPolicy",
            name: item.name,
            status: "Active",
            detail: `${item.ingressRules} ingress · ${item.egressRules} egress rules`,
            createdAt: item.createdAt,
            age: formatRelativeTime(item.createdAt),
            href: `/dashboard/clusters/${clusterId}/networkpolicies/${namespace}/${item.name}`,
          })),
      ],
    }),
    [
      clusterId,
      configMaps.data,
      ingresses.data,
      limits.data,
      namespace,
      namespaceEvents,
      namespacePods,
      policies.data,
      pvcs.data,
      quotas.data,
      roleBindings.data,
      roles.data,
      secrets.data,
      serviceAccounts.data,
      services.data,
      workloads.data?.data,
    ],
  );

  const loading = workloads.isLoading || pods.isLoading || namespaces.isLoading;
  const cpuPct = ns?.cpuLimit ? (ns.cpuUsage / ns.cpuLimit) * 100 : 0;
  const memoryPct = ns?.memoryLimit
    ? (ns.memoryUsage / ns.memoryLimit) * 100
    : 0;

  return (
    <div className="space-y-6">
      <section className="overflow-hidden rounded-xl border border-border bg-card">
        <div className="border-b border-border bg-gradient-to-r from-primary/10 via-card to-card px-6 py-5">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div>
              <div className="mb-2 flex items-center gap-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                <Boxes className="h-4 w-4" /> Namespace
              </div>
              {/* eslint-disable-line no-restricted-syntax -- migrated in plan 022 */}<h1 className="font-mono text-2xl font-semibold text-foreground">
                {namespace}
              </h1>
              <p className="mt-2 text-sm text-muted-foreground">
                {ns
                  ? `Created ${formatRelativeTime(ns.createdAt)}`
                  : "Namespace-scoped operations and resources"}
              </p>
            </div>
            <StatusBadge
              status={
                ns?.status ?? (namespaces.isLoading ? "Loading" : "Unknown")
              }
            />
          </div>
        </div>
        <div className="grid grid-cols-2 divide-x divide-y divide-border sm:grid-cols-3 lg:grid-cols-6 lg:divide-y-0">
          <Stat
            label="Workloads"
            value={workloads.data?.data.length ?? 0}
            icon={Boxes}
          />
          <Stat label="Pods" value={namespacePods.length} icon={Server} />
          <Stat
            label="Unhealthy"
            value={unhealthyPods.length}
            icon={TriangleAlert}
            danger={unhealthyPods.length > 0}
          />
          <Stat
            label="Restarts"
            value={restarts}
            icon={Activity}
            danger={restarts > 0}
          />
          <Stat
            label="Services"
            value={
              (services.data ?? []).filter(
                (item) => item.namespace === namespace,
              ).length
            }
            icon={Network}
          />
          <Stat
            label="Warning events"
            value={warningEvents.length}
            icon={TriangleAlert}
            danger={warningEvents.length > 0}
          />
        </div>
      </section>

      <nav
        className="flex gap-1 overflow-x-auto border-b border-border"
        aria-label="Namespace details"
      >
        {tabs.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => setTab(item.id)}
            className={cn(
              "shrink-0 border-b-2 px-3 py-2 text-sm font-medium",
              tab === item.id
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {item.label}
          </button>
        ))}
      </nav>

      {tab === "overview" && (
        <div className="grid gap-4 lg:grid-cols-3">
          <UsageCard
            title="CPU"
            value={formatCPU(ns?.cpuUsage ?? 0)}
            limit={ns?.cpuLimit ? formatCPU(ns.cpuLimit) : "No limit"}
            percent={cpuPct}
          />
          <UsageCard
            title="Memory"
            value={formatBytes(ns?.memoryUsage ?? 0)}
            limit={ns?.memoryLimit ? formatBytes(ns.memoryLimit) : "No limit"}
            percent={memoryPct}
          />
          <section className="rounded-lg border border-border bg-card p-4">
            <div className="mb-3 flex items-center gap-2 font-medium">
              <Gauge className="h-4 w-4" /> Operational signals
            </div>
            <Signal
              label="Pods healthy"
              value={`${namespacePods.length - unhealthyPods.length}/${namespacePods.length}`}
              good={unhealthyPods.length === 0}
            />
            <Signal
              label="Network policies"
              value={String(
                (policies.data ?? []).filter(
                  (item) => item.namespace === namespace,
                ).length,
              )}
              good={(policies.data ?? []).some(
                (item) => item.namespace === namespace,
              )}
            />
            <Signal
              label="PVCs bound"
              value={`${(pvcs.data ?? []).filter((item) => item.namespace === namespace && item.status === "Bound").length}/${(pvcs.data ?? []).filter((item) => item.namespace === namespace).length}`}
              good={
                !(pvcs.data ?? []).some(
                  (item) =>
                    item.namespace === namespace && item.status !== "Bound",
                )
              }
            />
          </section>
          <ResourceGroup
            title="Workloads"
            icon={Boxes}
            rows={rows.workloads}
            onOpen={() => setTab("workloads")}
          />
          <ResourceGroup
            title="Networking"
            icon={Network}
            rows={rows.networking}
            onOpen={() => setTab("networking")}
          />
          <ResourceGroup
            title="Configuration & policy"
            icon={ShieldCheck}
            rows={[...rows.configuration, ...rows.security]}
            onOpen={() => setTab("configuration")}
          />
          <section className="rounded-lg border border-border bg-card p-4 lg:col-span-3">
            <div className="mb-3 flex items-center gap-2 font-medium">
              <Braces className="h-4 w-4" /> Metadata
            </div>
            <div className="grid gap-4 md:grid-cols-2">
              <MetadataList label="Labels" values={ns?.labels} />
              <MetadataList label="Annotations" values={ns?.annotations} />
            </div>
          </section>
        </div>
      )}

      {tab === "usage" ? (
        <div className="grid gap-4 md:grid-cols-2">
          <UsageCard
            title="CPU usage"
            value={formatCPU(ns?.cpuUsage ?? 0)}
            limit={
              ns?.cpuLimit ? formatCPU(ns.cpuLimit) : "No configured limit"
            }
            percent={cpuPct}
          />
          <UsageCard
            title="Memory usage"
            value={formatBytes(ns?.memoryUsage ?? 0)}
            limit={
              ns?.memoryLimit
                ? formatBytes(ns.memoryLimit)
                : "No configured limit"
            }
            percent={memoryPct}
          />
        </div>
      ) : tab !== "overview" ? (
        <DataTable
          data={rows[tab]}
          columns={resourceColumns}
          keyExtractor={(row) => row.id}
          loading={loading}
          searchable
          searchPlaceholder={`Search ${tab} in ${namespace}…`}
          emptyState={{
            title: `No ${tab} resources found in ${namespace}`,
            description:
              "Resources will appear after the cluster reports them.",
          }}
          persistKey={`namespace:${tab}`}
        />
      ) : null}
    </div>
  );
}

function Stat({
  label,
  value,
  icon: Icon,
  danger = false,
}: {
  label: string;
  value: number;
  icon: typeof Boxes;
  danger?: boolean;
}) {
  return (
    <div className="p-4">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Icon className="h-3.5 w-3.5" />
        {label}
      </div>
      <div
        className={cn(
          "mt-1 text-2xl font-semibold tabular-nums",
          danger && "text-status-warning",
        )}
      >
        {value}
      </div>
    </div>
  );
}

function UsageCard({
  title,
  value,
  limit,
  percent,
}: {
  title: string;
  value: string;
  limit: string;
  percent: number;
}) {
  const bounded = Math.max(0, Math.min(percent, 100));
  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <div className="mb-3 flex items-center gap-2 font-medium">
        <Gauge className="h-4 w-4" />
        {title}
      </div>
      <div className="flex items-end justify-between">
        <span className="text-2xl font-semibold tabular-nums">{value}</span>
        <span className="text-xs text-muted-foreground">of {limit}</span>
      </div>
      <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={cn(
            "h-full rounded-full",
            bounded >= 90
              ? "bg-status-error"
              : bounded >= 75
                ? "bg-status-warning"
                : "bg-primary",
          )}
          style={{ width: `${bounded}%` }}
        />
      </div>
    </section>
  );
}

function Signal({
  label,
  value,
  good,
}: {
  label: string;
  value: string;
  good: boolean;
}) {
  return (
    <div className="flex items-center justify-between border-t border-border py-2 text-sm first:border-t-0">
      <span className="text-muted-foreground">{label}</span>
      <span
        className={cn(
          "font-medium tabular-nums",
          good ? "text-status-success" : "text-status-warning",
        )}
      >
        {value}
      </span>
    </div>
  );
}

function ResourceGroup({
  title,
  icon: Icon,
  rows,
  onOpen,
}: {
  title: string;
  icon: typeof Braces;
  rows: NamespaceResourceRow[];
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onOpen}
      className="rounded-lg border border-border bg-card p-4 text-left transition-colors hover:bg-accent/40"
    >
      <div className="flex items-center justify-between">
        <span className="flex items-center gap-2 font-medium">
          <Icon className="h-4 w-4" />
          {title}
        </span>
        <span className="text-2xl font-semibold tabular-nums">
          {rows.length}
        </span>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        Open the namespace-scoped {title.toLowerCase()} inventory
      </p>
    </button>
  );
}

function MetadataList({
  label,
  values,
}: {
  label: string;
  values?: Record<string, string>;
}) {
  const entries = Object.entries(values ?? {});
  return (
    <div>
      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      {entries.length === 0 ? (
        <p className="text-sm text-muted-foreground">None</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {entries.map(([key, value]) => (
            <span
              key={key}
              className="rounded border border-border bg-muted/40 px-2 py-1 font-mono text-2xs"
            >
              {key}={value}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
