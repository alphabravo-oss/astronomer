import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Select } from "@/components/ui/select";
import { PermissionState } from "@/components/ui/empty-state";
import { useBindingNames } from "@/components/rbac/use-binding-names";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { queryKeys } from "@/lib/query-keys";
import { getBindingPage } from "@/lib/api/rbac-binding-page";
import { pageTableCount } from "@/lib/api/pagination";
import type { RoleScope } from "@/lib/api/rbac";
import type { AccessBinding } from "@/types";
import { BindingsTab } from "./-bindings-tab";

export function PagedBindingsTab({
  onRevoke,
}: {
  onRevoke: (binding: AccessBinding) => void;
}) {
  const [scope, setScope] = useState<RoleScope>("global");
  const [pageIndex, setPageIndex] = useState(0);
  const read = usePermissionDecision("rbac", "read");
  const query = useQuery({
    queryKey: queryKeys.rbac.bindingPage(scope, pageIndex),
    queryFn: ({ signal }) => getBindingPage(scope, pageIndex * 25, signal),
    enabled: read.allowed,
    throwOnError: false,
  });
  const bindings =
    query.isError || !read.allowed ? [] : (query.data?.data ?? []);
  const names = useBindingNames(bindings, scope);
  if (!read.allowed) return <PermissionState permission="rbac:read" />;
  return (
    <div className="space-y-4">
      <Select
        aria-label="Binding scope"
        value={scope}
        onChange={(event) => {
          setScope(event.target.value as RoleScope);
          setPageIndex(0);
        }}
      >
        <option value="global">Global bindings</option>
        <option value="cluster">Cluster bindings</option>
        <option value="project">Project bindings</option>
      </Select>
      <p className="text-xs text-muted-foreground">
        Names are resolved for this page only. Unavailable or restricted names
        are shown as IDs.
      </p>
      <BindingsTab
        bindings={bindings}
        users={names.users}
        clusters={names.clusters}
        projects={names.projects}
        globalRoles={scope === "global" ? names.roles : []}
        clusterRoles={scope === "cluster" ? names.roles : []}
        projectRoles={scope === "project" ? names.roles : []}
        loading={query.isLoading}
        isError={query.isError}
        error={query.error}
        onRetry={() => void query.refetch()}
        onRevoke={onRevoke}
        serverSide={{
          ...pageTableCount(query.isError ? undefined : query.data),
          pagination: { pageIndex, pageSize: 25 },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
        }}
      />
    </div>
  );
}
