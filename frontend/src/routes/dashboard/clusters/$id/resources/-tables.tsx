import { DataTable, type Column } from "@/components/ui/data-table";
import { CircleCheck, CircleHelp, CircleX } from "lucide-react";
import {
  type MirroredGatewayClass,
  type MirroredIngressClass,
  type MirroredLimitRange,
  type MirroredNetworkPolicy,
  type MirroredResourceQuota,
} from "@/lib/api/cluster-resource-inventory";
import { ChipList } from "@/components/resources/networking-table-cells";
import {
  fmtRelative,
  LastSeen,
  LAST_SEEN_COLUMN,
  parseQuantity,
} from "./-shared";

export const ingressClassColumns: Column<MirroredIngressClass>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 200,
    accessor: (r) => <span className="font-mono">{r.name}</span>,
    searchAccessor: (r) => r.name,
    sortAccessor: (r) => r.name,
  },
  {
    key: "controller",
    header: "Controller",
    kind: "text",
    minSize: 224,
    size: 260,
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
    kind: "badge",
  },
  {
    ...LAST_SEEN_COLUMN,
    accessor: (r) => <LastSeen iso={r.lastSeenAt} />,
    sortAccessor: (r) => r.lastSeenAt || "",
  },
];

export function IngressClassesTable({
  rows,
}: {
  rows: MirroredIngressClass[];
}) {
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

export function AcceptedBadge({ status }: { status: string }) {
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

export const gatewayClassColumns: Column<MirroredGatewayClass>[] = [
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 200,
    accessor: (r) => <span className="font-mono">{r.name}</span>,
    searchAccessor: (r) => r.name,
    sortAccessor: (r) => r.name,
  },
  {
    key: "controller",
    header: "Controller",
    kind: "text",
    minSize: 224,
    size: 260,
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
    kind: "status",
  },
  {
    ...LAST_SEEN_COLUMN,
    accessor: (r) => <LastSeen iso={r.lastSeenAt} />,
    sortAccessor: (r) => r.lastSeenAt || "",
  },
];

export function GatewayClassesTable({
  rows,
}: {
  rows: MirroredGatewayClass[];
}) {
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

export const networkPolicyColumns: Column<MirroredNetworkPolicy>[] = [
  {
    key: "namespace",
    header: "Namespace",
    kind: "text",
    minSize: 136,
    size: 168,
    accessor: (r) => <span className="font-mono">{r.namespace}</span>,
    searchAccessor: (r) => r.namespace,
    sortAccessor: (r) => r.namespace,
    filter: { label: "Namespace" },
  },
  {
    key: "name",
    header: "Name",
    kind: "name",
    minSize: 200,
    accessor: (r) => <span className="font-mono">{r.name}</span>,
    searchAccessor: (r) => r.name,
    sortAccessor: (r) => r.name,
  },
  {
    key: "types",
    header: "Types",
    kind: "badge",
    minSize: 144,
    accessor: (r) => <ChipList items={r.policyTypes} mono={false} />,
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
    kind: "badge",
  },
  {
    ...LAST_SEEN_COLUMN,
    accessor: (r) => <LastSeen iso={r.lastSeenAt} />,
    sortAccessor: (r) => r.lastSeenAt || "",
  },
];

export function NetworkPoliciesTable({
  rows,
}: {
  rows: MirroredNetworkPolicy[];
}) {
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

export function QuotaProgressRow({
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

export function ResourceQuotasView({
  rows,
}: {
  rows: MirroredResourceQuota[];
}) {
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

export interface LimitRangeItem {
  type?: string;
  default?: Record<string, string>;
  defaultRequest?: Record<string, string>;
  max?: Record<string, string>;
  min?: Record<string, string>;
}

export const limitRangeItemColumns: Column<
  LimitRangeItem & { _key: number }
>[] = [
  {
    key: "type",
    header: "Type",
    kind: "text",
    minSize: 112,
    size: 112,
    accessor: (l) => <span className="font-mono">{l.type ?? "—"}</span>,
    sortAccessor: (l) => l.type ?? "",
  },
  {
    key: "default",
    header: "Default",
    kind: "text",
    grow: true,
    minSize: 200,
    accessor: (l) => <ChipList items={fmtItems(l.default)} empty="—" />,
  },
  {
    key: "defaultRequest",
    header: "Default request",
    kind: "text",
    minSize: 184,
    size: 184,
    accessor: (l) => <ChipList items={fmtItems(l.defaultRequest)} empty="—" />,
  },
  {
    key: "min",
    header: "Min",
    kind: "text",
    minSize: 152,
    size: 152,
    accessor: (l) => <ChipList items={fmtItems(l.min)} empty="—" />,
  },
  {
    key: "max",
    header: "Max",
    kind: "text",
    minSize: 152,
    size: 152,
    accessor: (l) => <ChipList items={fmtItems(l.max)} empty="—" />,
  },
];

export function LimitRangesTable({ rows }: { rows: MirroredLimitRange[] }) {
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
        const limits = ((r.limits ?? []) as LimitRangeItem[]).map((l, i) => ({
          ...l,
          _key: i,
        }));
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

export function fmtItems(m?: Record<string, string>): string[] {
  return Object.entries(m ?? {}).map(([k, v]) => `${k}=${v}`);
}

// ---------------------------------------------------------------------
// Page
