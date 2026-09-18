import type { ReactNode } from "react";

import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate } from "@tanstack/react-router";
import { detailHref, kindToResourceType } from "@/lib/k8s-paths";
import { toastPermissionDenied } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import type { Column } from "@/components/ui/data-table";

/** Workloads retain their purpose-built detail page. */
export function workloadDetailHref(
  clusterId: string,
  kind: string,
  namespace: string,
  name: string,
): string {
  return detailHref(clusterId, kindToResourceType(kind), namespace, name);
}

/** A visible, open-in-new-tab-compatible link into a resource detail route. */
export function NameLink({
  clusterId,
  resourceType,
  namespace,
  name,
}: {
  clusterId: string;
  resourceType: string;
  namespace?: string;
  name: string;
}) {
  return (
    <RouterLink
      to={detailHref(clusterId, resourceType, namespace, name)}
      onClick={(event) => event.stopPropagation()}
      className="app-table-entity-link app-table-primary"
    >
      {name}
    </RouterLink>
  );
}

/** Prevent action controls from also activating their containing table row. */
export function StopRowClick({ children }: { children: ReactNode }) {
  return <div data-row-click-ignore>{children}</div>;
}

export function nameColumn<T extends { name: string; namespace?: string }>(
  clusterId: string,
  resourceType: string,
): Column<T> {
  return {
    key: "name",
    header: "Name",
    accessor: (row) => (
      <NameLink
        clusterId={clusterId}
        resourceType={resourceType}
        namespace={row.namespace}
        name={row.name}
      />
    ),
    sortAccessor: (row) => row.name,
  };
}

/** Build an authorization-aware row drill-down callback. */
export function makeRowClick<T extends { name: string; namespace?: string }>(
  navigate: ReturnType<typeof useNavigate>,
  clusterId: string,
  resourceType: string,
  read: PermissionDecision,
) {
  return (row: T) => {
    if (!read.allowed) {
      toastPermissionDenied(read);
      return;
    }
    void navigate({
      to: detailHref(clusterId, resourceType, row.namespace, row.name),
    });
  };
}
