import { createFileRoute } from "@tanstack/react-router";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader } from "@/components/ui/page";
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

import { useId, useState } from "react";

import { useQuery } from "@tanstack/react-query";
import { QueryStates } from "@/components/ui/query-states";
import {
  ChevronDown,
  ChevronRight,
  CircleCheck,
  CircleHelp,
  CircleX,
  Layers,
  Network,
  Shield,
  Slash,
  SquareStack,
} from "lucide-react";

import {
  listMirroredGatewayClasses,
  listMirroredIngressClasses,
  listMirroredLimitRanges,
  listMirroredNetworkPolicies,
  listMirroredResourceQuotas,
  type MirroredGatewayClass,
  type MirroredIngressClass,
  type MirroredLimitRange,
  type MirroredNetworkPolicy,
  type MirroredResourceQuota,
} from "@/lib/api/cluster-resource-inventory";
import { queryKeys } from "@/lib/query-keys";

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

function fmtRelative(iso?: string): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const delta = Date.now() - t;
  const mins = Math.floor(delta / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

// parseQuantity converts a Kubernetes-style quantity string to a number
// where possible (so we can compute used/hard ratios). Returns NaN for
// anything we don't recognise — the UI degrades to a numerator/
// denominator string when that happens.
function parseQuantity(v: string | number | undefined | null): number {
  if (v == null) return NaN;
  if (typeof v === "number") return v;
  const m = /^(\d+(?:\.\d+)?)([a-zA-Z]*)$/.exec(v.trim());
  if (!m) return Number.NaN;
  const num = parseFloat(m[1]);
  const suffix = m[2];
  const mult: Record<string, number> = {
    "": 1,
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
    K: 1e3,
    M: 1e6,
    G: 1e9,
    T: 1e12,
    m: 1e-3,
  };
  return num * (mult[suffix] ?? 1);
}

// ---------------------------------------------------------------------
// Section primitive
// ---------------------------------------------------------------------

function Section({
  title,
  icon,
  count,
  defaultOpen = true,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  count: number;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const contentId = useId();
  return (
    <div className="rounded-lg border bg-card mb-4">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls={contentId}
        className="flex w-full items-center justify-between p-4 text-left"
      >
        <div className="flex items-center gap-2">
          {icon}
          <h2 className="text-base font-semibold">{title}</h2>
          <span className="rounded-full bg-muted px-2 py-0.5 text-xs">
            {count}
          </span>
        </div>
        {open ? (
          <ChevronDown className="h-4 w-4" />
        ) : (
          <ChevronRight className="h-4 w-4" />
        )}
      </button>
      {open && (
        <div id={contentId} className="border-t p-4">
          {children}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Per-kind tables
// ---------------------------------------------------------------------

const ingressClassColumns: Column<MirroredIngressClass>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (r) => <span className="font-mono">{r.name}</span>,
    searchAccessor: (r) => r.name,
    sortAccessor: (r) => r.name,
  },
  {
    key: "controller",
    header: "Controller",
    accessor: (r) => (
      <span className="font-mono text-xs">{r.controller || "—"}</span>
    ),
    searchAccessor: (r) => r.controller || "",
    sortAccessor: (r) => r.controller || "",
  },
  {
    key: "default",
    header: "Default",
    accessor: (r) =>
      r.isDefault ? (
        <span className="rounded-full bg-status-success/10 px-2 py-0.5 text-xs text-status-success">
          default
        </span>
      ) : (
        <span className="text-muted-foreground">—</span>
      ),
    sortAccessor: (r) => (r.isDefault ? 1 : 0),
    width: "8rem",
  },
  {
    key: "lastSeen",
    header: "Last seen",
    accessor: (r) => (
      <span className="text-muted-foreground">
        {fmtRelative(r.lastSeenAt)}
      </span>
    ),
    sortAccessor: (r) => r.lastSeenAt || "",
    width: "10rem",
  },
];

function IngressClassesTable({ rows }: { rows: MirroredIngressClass[] }) {
  return (
    <DataTable
      data={rows}
      columns={ingressClassColumns}
      keyExtractor={(r) => r.name}
      density="compact"
      searchPlaceholder="Search ingress classes..."
      emptyState={{
        title: "No IngressClasses installed",
        description: "IngressClasses will appear here once mirrored.",
      }}
    />
  );
}

function AcceptedBadge({ status }: { status: string }) {
  if (status === "True") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-status-success/10 px-2 py-0.5 text-xs text-status-success">
        <CircleCheck className="h-3 w-3" /> Accepted
      </span>
    );
  }
  if (status === "False") {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-status-error/10 px-2 py-0.5 text-xs text-status-error">
        <CircleX className="h-3 w-3" /> Rejected
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs text-foreground">
      <CircleHelp className="h-3 w-3" /> Unknown
    </span>
  );
}

const gatewayClassColumns: Column<MirroredGatewayClass>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (r) => <span className="font-mono">{r.name}</span>,
    searchAccessor: (r) => r.name,
    sortAccessor: (r) => r.name,
  },
  {
    key: "controller",
    header: "Controller",
    accessor: (r) => (
      <span className="font-mono text-xs">{r.controllerName || "—"}</span>
    ),
    searchAccessor: (r) => r.controllerName || "",
    sortAccessor: (r) => r.controllerName || "",
  },
  {
    key: "accepted",
    header: "Accepted",
    accessor: (r) => <AcceptedBadge status={r.acceptedStatus} />,
    searchAccessor: (r) => r.acceptedStatus,
    sortAccessor: (r) => r.acceptedStatus,
    filter: { label: "Accepted" },
    width: "9rem",
  },
  {
    key: "lastSeen",
    header: "Last seen",
    accessor: (r) => (
      <span className="text-muted-foreground">
        {fmtRelative(r.lastSeenAt)}
      </span>
    ),
    sortAccessor: (r) => r.lastSeenAt || "",
    width: "10rem",
  },
];

function GatewayClassesTable({ rows }: { rows: MirroredGatewayClass[] }) {
  return (
    <DataTable
      data={rows}
      columns={gatewayClassColumns}
      keyExtractor={(r) => r.name}
      density="compact"
      searchPlaceholder="Search gateway classes..."
      emptyState={{
        title: "No GatewayClasses installed",
        description: "GatewayClasses will appear here once mirrored.",
      }}
    />
  );
}

const networkPolicyColumns: Column<MirroredNetworkPolicy>[] = [
  {
    key: "namespace",
    header: "Namespace",
    accessor: (r) => <span className="font-mono">{r.namespace}</span>,
    searchAccessor: (r) => r.namespace,
    sortAccessor: (r) => r.namespace,
    filter: { label: "Namespace" },
  },
  {
    key: "name",
    header: "Name",
    accessor: (r) => <span className="font-mono">{r.name}</span>,
    searchAccessor: (r) => r.name,
    sortAccessor: (r) => r.name,
  },
  {
    key: "types",
    header: "Types",
    accessor: (r) => (
      <>
        {(r.policyTypes ?? []).map((t) => (
          <span
            key={t}
            className="mr-1 rounded-full bg-muted px-2 py-0.5 text-xs"
          >
            {t}
          </span>
        ))}
      </>
    ),
    searchAccessor: (r) => (r.policyTypes ?? []).join(" "),
  },
  {
    key: "owner",
    header: "Owner",
    accessor: (r) =>
      r.isManaged ? (
        <span className="rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary">
          astronomer
        </span>
      ) : (
        <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
          operator
        </span>
      ),
    searchAccessor: (r) => (r.isManaged ? "astronomer" : "operator"),
    sortAccessor: (r) => (r.isManaged ? "astronomer" : "operator"),
    filter: { label: "Owner" },
    width: "8rem",
  },
  {
    key: "lastSeen",
    header: "Last seen",
    accessor: (r) => (
      <span className="text-muted-foreground">
        {fmtRelative(r.lastSeenAt)}
      </span>
    ),
    sortAccessor: (r) => r.lastSeenAt || "",
    width: "10rem",
  },
];

function NetworkPoliciesTable({ rows }: { rows: MirroredNetworkPolicy[] }) {
  return (
    <DataTable
      data={rows}
      columns={networkPolicyColumns}
      keyExtractor={(r) => `${r.namespace}/${r.name}`}
      density="compact"
      searchPlaceholder="Search network policies..."
      emptyState={{
        title: "No NetworkPolicies in this cluster",
        description: "NetworkPolicies will appear here once mirrored.",
      }}
    />
  );
}

function QuotaProgressRow({
  label,
  hard,
  used,
}: {
  label: string;
  hard: string | undefined;
  used: string | undefined;
}) {
  const hardN = parseQuantity(hard);
  const usedN = parseQuantity(used);
  const pct =
    Number.isFinite(hardN) && Number.isFinite(usedN) && hardN > 0
      ? Math.min(100, Math.round((usedN / hardN) * 100))
      : null;
  const barColor =
    pct == null
      ? "bg-muted-foreground"
      : pct > 90
        ? "bg-status-error"
        : pct > 75
          ? "bg-status-warning"
          : "bg-status-success";
  return (
    <div className="mb-2">
      <div className="flex items-center justify-between text-xs">
        <span className="font-mono">{label}</span>
        <span className="text-muted-foreground">
          {used ?? "0"} / {hard ?? "—"}
          {pct != null ? ` (${pct}%)` : ""}
        </span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-sm bg-muted">
        <div
          className={`h-full ${barColor}`}
          style={{ width: pct == null ? "0%" : `${pct}%` }}
        />
      </div>
    </div>
  );
}

function ResourceQuotasView({ rows }: { rows: MirroredResourceQuota[] }) {
  if (rows.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No ResourceQuotas in this cluster.
      </p>
    );
  }
  return (
    <div className="space-y-4">
      {rows.map((r) => {
        const hardEntries = Object.entries(r.hard ?? {});
        return (
          <div
            key={`${r.namespace}/${r.name}`}
            className="rounded-sm border p-3"
          >
            <div className="mb-2 flex items-center justify-between">
              <div>
                <span className="font-mono text-sm">
                  {r.namespace}/{r.name}
                </span>
              </div>
              <span className="text-xs text-muted-foreground">
                {fmtRelative(r.lastSeenAt)}
              </span>
            </div>
            {hardEntries.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No hard limits set.
              </p>
            ) : (
              hardEntries.map(([k, v]) => (
                <QuotaProgressRow
                  key={k}
                  label={k}
                  hard={v}
                  used={r.used?.[k]}
                />
              ))
            )}
          </div>
        );
      })}
    </div>
  );
}

interface LimitRangeItem {
  type?: string;
  default?: Record<string, string>;
  defaultRequest?: Record<string, string>;
  max?: Record<string, string>;
  min?: Record<string, string>;
}

const limitRangeItemColumns: Column<LimitRangeItem & { _key: number }>[] = [
  {
    key: "type",
    header: "Type",
    accessor: (l) => <span className="font-mono">{l.type ?? "—"}</span>,
    sortAccessor: (l) => l.type ?? "",
  },
  {
    key: "default",
    header: "Default",
    accessor: (l) => <span className="font-mono">{fmtMap(l.default)}</span>,
  },
  {
    key: "defaultRequest",
    header: "DefaultRequest",
    accessor: (l) => (
      <span className="font-mono">{fmtMap(l.defaultRequest)}</span>
    ),
  },
  {
    key: "min",
    header: "Min",
    accessor: (l) => <span className="font-mono">{fmtMap(l.min)}</span>,
  },
  {
    key: "max",
    header: "Max",
    accessor: (l) => <span className="font-mono">{fmtMap(l.max)}</span>,
  },
];

function LimitRangesTable({ rows }: { rows: MirroredLimitRange[] }) {
  if (rows.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No LimitRanges in this cluster.
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {rows.map((r) => {
        const limits = ((r.limits ?? []) as LimitRangeItem[]).map(
          (l, i) => ({ ...l, _key: i }),
        );
        return (
          <div
            key={`${r.namespace}/${r.name}`}
            className="rounded-sm border p-3"
          >
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-sm">
                {r.namespace}/{r.name}
              </span>
              <span className="text-xs text-muted-foreground">
                {fmtRelative(r.lastSeenAt)}
              </span>
            </div>
            <DataTable
              data={limits}
              columns={limitRangeItemColumns}
              keyExtractor={(l) => String(l._key)}
              density="compact"
              searchable={false}
              emptyState={{
                title: "No limits defined",
                description: "This LimitRange has no limit entries.",
              }}
            />
          </div>
        );
      })}
    </div>
  );
}

function fmtMap(m?: Record<string, string>): string {
  if (!m) return "—";
  const keys = Object.keys(m);
  if (keys.length === 0) return "—";
  return keys.map((k) => `${k}=${m[k]}`).join(", ");
}

// ---------------------------------------------------------------------
// Page
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
    <div className="p-6">
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
    </div>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/resources/")({
  component: ClusterResourcesPage,
});
