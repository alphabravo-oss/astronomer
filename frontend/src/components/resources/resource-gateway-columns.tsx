/**
 * Column definitions for Gateways, Gateway routes, GatewayClasses and
 * ReferenceGrants. Every column declares a semantic `kind` (plan 031 P6b).
 */
import type { Column } from "@/components/ui/data-table";
import {
  ageColumn,
  ChipList,
  countColumn,
  monoTextColumn,
  nameStubColumn,
  namespaceColumn,
} from "@/components/resources/networking-table-cells";
import type {
  Gateway,
  GatewayClass,
  GatewayRoute,
  ReferenceGrant,
} from "@/types";

function ConditionPill({
  status,
  trueLabel,
  falseLabel,
}: {
  status: string;
  trueLabel: string;
  falseLabel: string;
}) {
  if (!status) return <span className="text-xs text-muted-foreground">—</span>;
  if (status === "True") {
    return (
      <span className="text-xs px-1.5 py-0.5 rounded-sm bg-status-success/10 text-status-success">
        {trueLabel}
      </span>
    );
  }
  if (status === "False") {
    return (
      <span className="text-xs px-1.5 py-0.5 rounded-sm bg-status-error/10 text-status-error">
        {falseLabel}
      </span>
    );
  }
  return (
    <span className="text-xs px-1.5 py-0.5 rounded-sm bg-muted text-muted-foreground">
      {status}
    </span>
  );
}

function conditionColumn<T>(
  key: string,
  header: string,
  value: (row: T) => string,
  trueLabel: string,
  falseLabel: string,
): Column<T> {
  return {
    key,
    header,
    kind: "status",
    accessor: (row) => (
      <ConditionPill
        status={value(row)}
        trueLabel={trueLabel}
        falseLabel={falseLabel}
      />
    ),
    sortAccessor: value,
  };
}

export const gatewayColumns: Column<Gateway>[] = [
  nameStubColumn<Gateway>(112),
  namespaceColumn<Gateway>(),
  monoTextColumn<Gateway>("class", "Class", (r) => r.gatewayClassName, {
    minSize: 96,
    size: 96,
  }),
  {
    key: "listeners",
    header: "Listeners",
    kind: "badge",
    minSize: 176,
    size: 176,
    maxSize: 320,
    accessor: (row) => <ChipList items={row.listenerSummary} />,
    searchAccessor: (row) => row.listenerSummary?.join(" ") ?? "",
    sortable: false,
  },
  monoTextColumn<Gateway>(
    "addresses",
    "Addresses",
    (r) => r.addresses?.join(", ") ?? "",
    { minSize: 120, size: 120, sortable: false },
  ),
  conditionColumn<Gateway>(
    "programmed",
    "Programmed",
    (r) => r.programmed,
    "Programmed",
    "Failed",
  ),
  ageColumn<Gateway>(),
];

// Shared by HTTPRoute / GRPCRoute / TLSRoute / TCPRoute / UDPRoute. The L4
// routes (TCP/UDP) won't populate hostnames; that cell just renders "-".
export const routeColumns: Column<GatewayRoute>[] = [
  nameStubColumn<GatewayRoute>(),
  namespaceColumn<GatewayRoute>(),
  {
    key: "parents",
    header: "Parent Gateways",
    kind: "badge",
    minSize: 190,
    size: 190,
    maxSize: 320,
    accessor: (row) => <ChipList items={row.parentSummary} />,
    searchAccessor: (row) => row.parentSummary?.join(" ") ?? "",
    sortable: false,
  },
  {
    key: "hostnames",
    header: "Hostnames",
    kind: "text",
    minSize: 190,
    size: 190,
    accessor: (row) => <ChipList items={row.hostnames} />,
    searchAccessor: (row) => row.hostnames?.join(" ") ?? "",
    sortable: false,
  },
  countColumn<GatewayRoute>("rules", "Rules", (r) => r.ruleCount),
  ageColumn<GatewayRoute>(),
];

export const gatewayClassColumns: Column<GatewayClass>[] = [
  nameStubColumn<GatewayClass>(),
  monoTextColumn<GatewayClass>(
    "controllerName",
    "Controller",
    (r) => r.controllerName,
    { minSize: 240, size: 260, maxSize: 520 },
  ),
  conditionColumn<GatewayClass>(
    "accepted",
    "Accepted",
    (r) => r.accepted,
    "Accepted",
    "Rejected",
  ),
  {
    key: "description",
    header: "Description",
    kind: "text",
    minSize: 200,
    size: 220,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground">
        {row.description || "-"}
      </span>
    ),
    sortAccessor: (row) => row.description ?? "",
    sortable: false,
  },
  ageColumn<GatewayClass>(),
];

export const referenceGrantColumns: Column<ReferenceGrant>[] = [
  nameStubColumn<ReferenceGrant>(),
  namespaceColumn<ReferenceGrant>(),
  {
    key: "from",
    header: "From",
    kind: "badge",
    minSize: 190,
    size: 190,
    maxSize: 320,
    accessor: (row) => (
      <ChipList items={row.from?.map((f) => `${f.kind}@${f.namespace}`)} />
    ),
    searchAccessor: (row) =>
      row.from?.map((f) => `${f.kind} ${f.namespace}`).join(" ") ?? "",
    sortable: false,
  },
  {
    key: "to",
    header: "To",
    kind: "badge",
    minSize: 190,
    size: 190,
    maxSize: 320,
    accessor: (row) => (
      <ChipList
        items={row.to?.map((t) => `${t.kind}${t.name ? `/${t.name}` : ""}`)}
      />
    ),
    searchAccessor: (row) =>
      row.to?.map((t) => `${t.kind} ${t.name ?? ""}`).join(" ") ?? "",
    sortable: false,
  },
  ageColumn<ReferenceGrant>(),
];
