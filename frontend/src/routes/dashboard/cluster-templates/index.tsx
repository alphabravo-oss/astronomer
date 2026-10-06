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
import {
  EmptyState,
  PermissionState,
  type EmptyStateActionProps,
} from "@/components/ui/empty-state";
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
import { EntityCell } from "@/components/tables/cells";
import type { ClusterTemplate } from "@/lib/api/project-detail";
import { BareButton } from "@/components/form/bare-button";

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
              You need permission to view cluster templates. Ask an
              administrator to grant the role.
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
      kind: "name",
      minSize: 240,
      accessor: (row) => (
        <div className="flex min-w-0 items-center gap-2">
          <Layers className="h-4 w-4 shrink-0 text-muted-foreground" />
          <EntityCell
            primary={row.displayName}
            secondary={<span className="font-mono">{row.name}</span>}
          />
        </div>
      ),
    },
    {
      key: "description",
      header: "Description",
      kind: "text",
      size: 280,
      minSize: 200,
      maxSize: 520,
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.description || "—"}
        </span>
      ),
      sortable: false,
    },
    {
      key: "environment",
      header: "Environment",
      kind: "badge",
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded-sm bg-muted text-muted-foreground capitalize">
          {row.spec.environment}
        </span>
      ),
      sortAccessor: (row) => row.spec.environment,
    },
    {
      key: "clusters",
      header: "Clusters",
      kind: "count",
      size: 120,
      accessor: (row) => (
        <span className="text-sm tabular-nums">{row.clustersBound}</span>
      ),
      sortAccessor: (row) => row.clustersBound,
    },
    {
      key: "createdBy",
      header: "Created by",
      kind: "text",
      size: 170,
      minSize: 150,
      accessor: (row) => (
        <EntityCell
          primary={row.createdBy || "—"}
          primaryClassName="font-normal text-xs text-muted-foreground"
          secondary={formatRelativeTime(row.createdAt)}
        />
      ),
      sortable: false,
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      accessor: (row) => (
        <div className="flex items-center gap-1 justify-end">
          {canWrite && (
            <BareButton
              aria-label="Delete template"
              onClick={() => setDeleteTarget(row)}
              className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
              tooltip="Delete template"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </BareButton>
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
            {...(canWrite
              ? ({
                  actionLabel: "Create bundle",
                  actionIcon: Plus,
                  onAction: () =>
                    void navigate({ to: "/dashboard/cluster-templates/new" }),
                } satisfies EmptyStateActionProps)
              : // terminal: read-only viewers can't create a bundle from here.
                { terminal: true })}
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
