import { createFileRoute } from "@tanstack/react-router";
/**
 * Cluster Templates — top-level list page.
 *
 * A cluster template captures the "shape" of a managed cluster: environment
 * tag, labels, tools (with presets + values overrides), default project
 * policy, and a registration policy. New clusters can be bootstrapped from
 * a template to inherit all of the above.
 *
 * The list is read-gated on `cluster_templates:read` and the
 * create/edit/delete actions are gated on `cluster_templates:write`. When
 * the user lacks read we still mount the page — we just render an explainer
 * instead of the list, so the sidebar link remains a stable target.
 */
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Plus, Trash2, Layers } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState, PermissionState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { PageHeader, PageShell } from "@/components/ui/page";
import { useCurrentUser } from "@/lib/hooks/auth";
import {
  useClusterTemplates,
  useDeleteClusterTemplate,
  canReadClusterTemplates,
  canWriteClusterTemplates,
} from "@/components/projects/hooks";
import { formatRelativeTime } from "@/lib/utils";
import type { ClusterTemplate } from "@/lib/api/project-detail";

function ClusterTemplatesPage() {
  const navigate = useNavigate();
  const { data: user } = useCurrentUser();
  const canRead = canReadClusterTemplates(user);
  const canWrite = canWriteClusterTemplates(user);

  const templatesQuery = useClusterTemplates();
  const deleteMutation = useDeleteClusterTemplate();
  const [deleteTarget, setDeleteTarget] = useState<ClusterTemplate | null>(
    null,
  );

  const templates = templatesQuery.data?.data || [];

  if (!canRead) {
    return (
      <PageShell>
        <PageHeader title="Onboarding Bundles" />
        <PermissionState
          permission="cluster_templates:read"
          description={
            <>
              You need <span className="font-mono">cluster_templates:read</span>{" "}
              to view templates. Ask an administrator to grant the role.
            </>
          }
          className="rounded-lg border border-border bg-muted/30 p-6"
        />
      </PageShell>
    );
  }

  const columns: Column<ClusterTemplate>[] = [
    {
      key: "name",
      header: "Template",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Layers className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.displayName}</p>
            <p className="text-xs text-muted-foreground font-mono">
              {row.name}
            </p>
          </div>
        </div>
      ),
    },
    {
      key: "description",
      header: "Description",
      accessor: (row) => (
        <span className="text-sm text-muted-foreground truncate max-w-[320px] block">
          {row.description || "—"}
        </span>
      ),
      sortable: false,
    },
    {
      key: "environment",
      header: "Environment",
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground capitalize">
          {row.spec.environment}
        </span>
      ),
      sortAccessor: (row) => row.spec.environment,
    },
    {
      key: "clusters",
      header: "Clusters bound",
      accessor: (row) => (
        <span className="text-sm tabular-nums">{row.clustersBound}</span>
      ),
      sortAccessor: (row) => row.clustersBound,
      align: "center",
    },
    {
      key: "createdBy",
      header: "Created by",
      accessor: (row) => (
        <div className="text-xs text-muted-foreground">
          <p>{row.createdBy || "—"}</p>
          <p>{formatRelativeTime(row.createdAt)}</p>
        </div>
      ),
      sortable: false,
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1 justify-end">
          {canWrite && (
            <button
              type="button"
              onClick={() => setDeleteTarget(row)}
              className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
              title="Delete template"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="Onboarding Bundles"
        description="Reusable bundles of tools, policy, and project defaults applied to a cluster after you register it — they shape an adopted cluster, they don't create one."
        actions={
          canWrite ? (
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() =>
                void navigate({ to: "/dashboard/cluster-templates/new" })
              }
            >
              New bundle
            </ActionButton>
          ) : undefined
        }
      />

      <QueryStates
        query={templatesQuery}
        loadingTitle="Loading onboarding bundles"
        permission="cluster_templates:read"
        errorTitle="Failed to load onboarding bundles"
        isEmpty={(result) => result.data.length === 0}
        empty={
          <EmptyState
            icon={Layers}
            title="No onboarding bundles yet"
            description="Create a reusable bundle of tools, policy, and project defaults for adopted clusters."
            actionLabel={canWrite ? "Create bundle" : undefined}
            actionIcon={Plus}
            onAction={
              canWrite
                ? () =>
                    void navigate({ to: "/dashboard/cluster-templates/new" })
                : undefined
            }
          />
        }
      >
        <DataTable
          data={templates}
          columns={columns}
          keyExtractor={(row) => row.id}
          searchPlaceholder="Search templates..."
          emptyState={{
            title: "No onboarding bundles available",
            description:
              "Resources will appear here when they are available in this scope.",
          }}
          onRowClick={(row) =>
            void navigate({ to: `/dashboard/cluster-templates/${row.id}` })
          }
        />
      </QueryStates>
      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!deleteTarget) return;
          deleteMutation.mutate(deleteTarget.id, {
            onSuccess: () => setDeleteTarget(null),
          });
        }}
        title="Delete onboarding bundle"
        description="This permanently deletes the reusable onboarding configuration."
        confirmValue={deleteTarget?.displayName}
        variant="destructive"
        loading={deleteMutation.isPending}
        impact={
          deleteTarget
            ? {
                scope: deleteTarget.displayName,
                consequences: [
                  "The bundle can no longer be selected for newly adopted clusters.",
                  `${deleteTarget.clustersBound} currently bound cluster${deleteTarget.clustersBound === 1 ? "" : "s"} keep their existing configuration.`,
                ],
                recovery: "Recreate the onboarding bundle manually.",
              }
            : undefined
        }
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/cluster-templates/")({
  component: ClusterTemplatesPage,
});
