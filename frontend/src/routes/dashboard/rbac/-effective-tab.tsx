import { useMemo, useState } from "react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import { RemoteUserPicker } from "@/components/rbac/remote-user-picker";
import { ActionButton } from "@/components/ui/action-button";
import { MetricCard } from "@/components/ui/metric-card";
import { useCluster } from "@/lib/hooks/clusters";
import { useProject } from "@/lib/hooks/projects";
import { useEffectivePermissions } from "@/lib/hooks/rbac";
import type {
  EffectivePermissionBinding,
  EffectivePermissionGrant,
  EffectivePermissionSource,
} from "@/types";
import { clusterLabel, projectLabel } from "@/components/rbac/binding-utils";

function effectivePermissionColumns(
  clusterNameById: Map<string, string>,
  projectNameById: Map<string, string>,
): Column<EffectivePermissionGrant>[] {
  return [
    {
      key: "applies",
      header: "Applies",
      accessor: (row) => (
        <Badge
          variant={row.appliesToContext === false ? "secondary" : "success"}
        >
          {row.appliesToContext === false ? "No" : "Yes"}
        </Badge>
      ),
      sortAccessor: (row) => (row.appliesToContext === false ? 0 : 1),
    },
    {
      key: "resource",
      header: "Resource",
      accessor: (row) => (
        <span className="font-mono text-sm">{row.resource}</span>
      ),
      sortAccessor: (row) => row.resource,
    },
    {
      key: "verb",
      header: "Verb",
      accessor: (row) => <span className="font-mono text-sm">{row.verb}</span>,
      sortAccessor: (row) => row.verb,
    },
    {
      key: "risk",
      header: "Risk",
      accessor: (row) => (
        <Badge variant={riskVariant(row)}>{riskLabel(row)}</Badge>
      ),
      sortAccessor: (row) => riskSort(row),
    },
    {
      key: "sources",
      header: "Granted By",
      accessor: (row) => sourceSummary(row.sources),
      sortable: false,
    },
    {
      key: "target",
      header: "Scope Target",
      accessor: (row) =>
        targetSummary(row.sources, clusterNameById, projectNameById),
      sortable: false,
    },
  ];
}

export function EffectiveTab() {
  const [userId, setUserId] = useState("");
  const [clusterId, setClusterId] = useState("");
  const [projectId, setProjectId] = useState("");
  const [namespace, setNamespace] = useState("");
  const clusterQuery = useCluster(clusterId);
  const projectQuery = useProject(projectId);

  const selectedContext = {
    clusterId: clusterId || undefined,
    projectId: projectId || undefined,
    namespace: namespace.trim() || undefined,
  };
  const {
    data: response,
    isLoading,
    isError,
    refetch,
  } = useEffectivePermissions(userId || undefined, selectedContext);

  const data = isError ? undefined : response;
  const measured = !isLoading && !isError && !!data;
  const permissions = data?.permissions ?? [];
  const bindings = data?.bindings ?? [];
  const responseContext = data?.context;
  const superuser = data?.superuser === true;
  const resourceCount = new Set(permissions.map((p) => p.resource)).size;
  const highRiskCount = permissions.filter(isHighRiskGrant).length;
  const applicableCount = permissions.filter(
    (p) => p.appliesToContext !== false,
  ).length;

  const clusterNameById = useMemo(
    () =>
      new Map(
        clusterQuery.data && !clusterQuery.isError
          ? [[clusterQuery.data.id, clusterLabel(clusterQuery.data)]]
          : [],
      ),
    [clusterQuery.data, clusterQuery.isError],
  );
  const projectNameById = useMemo(
    () =>
      new Map(
        projectQuery.data && !projectQuery.isError
          ? [[projectQuery.data.id, projectLabel(projectQuery.data)]]
          : [],
      ),
    [projectQuery.data, projectQuery.isError],
  );

  const permissionColumns = effectivePermissionColumns(
    clusterNameById,
    projectNameById,
  );

  const bindingColumns: Column<EffectivePermissionBinding>[] = [
    {
      key: "role",
      header: "Role",
      accessor: (row) =>
        row.roleName || row.roleId || row.bindingId || row.scope,
    },
    {
      key: "scope",
      header: "Scope",
      accessor: (row) => (
        <Badge
          variant={row.superuser ? "warning" : "secondary"}
          className="capitalize"
        >
          {row.superuser ? "superuser" : row.scope || "global"}
        </Badge>
      ),
    },
    {
      key: "target",
      header: "Target",
      accessor: (row) =>
        namedBindingTarget(row, clusterNameById, projectNameById),
      sortable: false,
    },
    {
      key: "rules",
      header: "Rules",
      accessor: (row) => (
        <span className="tabular-nums">{row.rules?.length ?? 0}</span>
      ),
      sortAccessor: (row) => row.rules?.length ?? 0,
      align: "center",
    },
  ];

  return (
    <div className="space-y-4">
      {superuser && (
        <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 px-4 py-3">
          <p className="text-sm font-medium text-foreground">Superuser</p>
          <p className="mt-0.5 text-sm text-muted-foreground">
            This account bypasses role checks and is granted every permission on
            the platform.
          </p>
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <MetricCard
          dense
          label="Grants"
          value={measured ? permissions.length : "—"}
        />
        <MetricCard
          dense
          label="Bindings"
          value={measured ? bindings.length : "—"}
        />
        <MetricCard
          dense
          label="Resources"
          value={measured ? resourceCount : "—"}
        />
        <MetricCard
          dense
          label="Applies here"
          value={measured ? applicableCount : "—"}
        />
        <MetricCard
          dense
          label="High risk"
          value={measured ? highRiskCount : "—"}
          tone={highRiskCount > 0 ? "warning" : undefined}
        />
      </div>

      <div className="grid gap-3 rounded-lg border border-border bg-card p-4 md:grid-cols-2 xl:grid-cols-4">
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">User</p>
          <RemoteUserPicker value={userId} onChange={setUserId} />
        </div>
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">Cluster</p>
          <RemoteClusterPicker
            value={clusterId}
            onChange={setClusterId}
            placeholder="All clusters"
          />
          <ActionButton onClick={() => setClusterId("")}>
            All clusters
          </ActionButton>
        </div>
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">Project</p>
          <RemoteProjectPicker value={projectId} onChange={setProjectId} />
          <ActionButton onClick={() => setProjectId("")}>
            All projects
          </ActionButton>
        </div>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">
            Namespace
          </span>
          <Input
            value={namespace}
            onChange={(event) => setNamespace(event.target.value)}
            placeholder="optional"
            className="font-mono"
          />
        </label>
        {responseContext?.warnings?.length ? (
          <p className="text-xs text-muted-foreground md:col-span-2 xl:col-span-4">
            {responseContext.warnings.join(" ")}
          </p>
        ) : null}
      </div>

      <DataTable
        data={permissions}
        columns={permissionColumns}
        keyExtractor={(row) => `${row.resource}:${row.verb}`}
        searchPlaceholder="Search effective permissions..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyState={{
          title: "No effective permissions found",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        pageSize={25}
      />

      <DataTable
        data={bindings}
        columns={bindingColumns}
        keyExtractor={(row) =>
          row.bindingId ||
          `${row.scope}:${row.roleId || row.roleName || "binding"}`
        }
        searchPlaceholder="Search permission sources..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyState={{
          title: "No role bindings contribute permissions",
          description:
            "Resources will appear here when they are available in this scope.",
        }}
        pageSize={10}
      />
    </div>
  );
}

function sourceSummary(sources?: EffectivePermissionSource[]): string {
  const labels = (sources ?? []).map(
    (source) =>
      source.roleName ||
      source.roleId ||
      source.bindingId ||
      source.scope ||
      "binding",
  );
  return unique(labels).join(", ") || "—";
}

function targetSummary(
  sources: EffectivePermissionSource[] | undefined,
  clusters: Map<string, string>,
  projects: Map<string, string>,
): string {
  const labels = (sources ?? []).map((source) =>
    namedSourceTarget(source, clusters, projects),
  );
  return unique(labels).join(", ") || "—";
}

function namedBindingTarget(
  binding: EffectivePermissionBinding,
  clusters: Map<string, string>,
  projects: Map<string, string>,
): string {
  return namedSourceTarget(binding, clusters, projects);
}

function namedSourceTarget(
  source: {
    clusterId?: string;
    projectId?: string;
    namespace?: string;
    scope?: string;
  },
  clusters: Map<string, string>,
  projects: Map<string, string>,
): string {
  if (source.projectId)
    return projects.get(source.projectId) || `project:${source.projectId}`;
  if (source.clusterId) {
    const name = clusters.get(source.clusterId) || source.clusterId;
    return source.namespace ? `${name} / ${source.namespace}` : name;
  }
  return source.scope || "global";
}

function unique(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean)));
}

function isHighRiskGrant(grant: EffectivePermissionGrant): boolean {
  return riskSort(grant) >= 2;
}

function riskSort(grant: EffectivePermissionGrant): number {
  if (grant.resource === "*" || grant.verb === "*") return 3;
  if (
    grant.resource === "secrets" &&
    ["read", "list", "watch"].includes(grant.verb)
  )
    return 3;
  if (["delete", "manage", "exec", "proxy", "sync"].includes(grant.verb))
    return 2;
  if (["create", "update", "scale", "restart"].includes(grant.verb)) return 1;
  return 0;
}

function riskLabel(grant: EffectivePermissionGrant): string {
  const risk = riskSort(grant);
  if (risk >= 3) return "Critical";
  if (risk === 2) return "High";
  if (risk === 1) return "Medium";
  return "Low";
}

function riskVariant(
  grant: EffectivePermissionGrant,
): "error" | "warning" | "info" | "secondary" {
  const risk = riskSort(grant);
  if (risk >= 3) return "error";
  if (risk === 2) return "warning";
  if (risk === 1) return "info";
  return "secondary";
}
