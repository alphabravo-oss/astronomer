import type { ReactNode } from "react";

import { Link } from "@/lib/link";
import { useRouter } from "@/lib/navigation";
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
    <Link
      href={detailHref(clusterId, resourceType, namespace, name)}
      onClick={(event) => event.stopPropagation()}
      className="font-medium text-foreground font-mono text-xs hover:underline"
    >
      {name}
    </Link>
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
  router: ReturnType<typeof useRouter>,
  clusterId: string,
  resourceType: string,
  read: PermissionDecision,
) {
  return (row: T) => {
    if (!read.allowed) {
      toastPermissionDenied(read);
      return;
    }
    router.push(detailHref(clusterId, resourceType, row.namespace, row.name));
  };
}
