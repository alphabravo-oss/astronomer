import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/cluster-groups — operator-defined folder hierarchy
 * over clusters (migration 066).
 *
 * The list is rendered as a flat depth-annotated table — the indent on
 * the name column shows hierarchy without the complexity of a real
 * tree-table widget. Top-level groups have depth 0; nested groups
 * indent by their depth. The depth cap is 2 (root + 2 levels) enforced
 * server-side; the form's parent picker grays out options that would
 * push the new group past the cap.
 *
 * Read/write is gated by clusters:update — group admin is a clusters-
 * admin concept, not a settings concept; the page sits under /settings/
 * because that's where the other operator-facing CRUDs live.
 */
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { useAppForm, useStore } from "@/lib/form";
import { Plus, Trash2, Pencil, Folder, Server } from "lucide-react";
import * as api from "@/lib/api";
import { queryKeys, useClusters } from "@/lib/hooks";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import {
  CLUSTER_GROUP_COLORS,
  CLUSTER_GROUP_ICONS,
  type ClusterGroupTreeNode,
  type ClusterGroupWriteRequest,
  listClustersInGroup,
  moveClustersToGroup,
} from "@/lib/api/cluster-groups";

const MAX_DEPTH = 2;

function useClusterGroups() {
  return useQuery({
    queryKey: queryKeys.clusterGroups.all,
    queryFn: ({ signal }) => api.listClusterGroups({ signal }),
  });
}

function useCreateClusterGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ClusterGroupWriteRequest) =>
      api.createClusterGroup(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.clusterGroups.all });
      toastSuccess("Cluster group created");
    },
    onError: (err: Error) => toastApiError("Failed to create", err),
  });
}

function useUpdateClusterGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      body,
    }: {
      id: string;
      body: ClusterGroupWriteRequest;
    }) => api.updateClusterGroup(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.clusterGroups.all });
      toastSuccess("Cluster group updated");
    },
    onError: (err: Error) => toastApiError("Failed to update", err),
  });
}

function useDeleteClusterGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteClusterGroup(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.clusterGroups.all });
      toastSuccess("Cluster group deleted");
    },
    onError: (err: Error) => toastApiError("Failed to delete", err),
  });
}

function ClusterGroupsPage() {
  const { data, isLoading } = useClusterGroups();
  const createMut = useCreateClusterGroup();
  const updateMut = useUpdateClusterGroup();
  const deleteMut = useDeleteClusterGroup();

  const [editing, setEditing] = useState<ClusterGroupTreeNode | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [selected, setSelected] = useState<ClusterGroupTreeNode | null>(null);

  const tree = useMemo(() => data ?? [], [data]);

  // Sort the flat tree by parent → depth → name so siblings cluster
  // together visually. The server already returns rows ordered by
  // (depth, name) — we re-shape them into a parent-grouped view here.
  const flattened = useMemo(() => {
    const byParent: Record<string, ClusterGroupTreeNode[]> = {};
    for (const node of tree) {
      const key = node.parentId ?? "__root__";
      byParent[key] = byParent[key] || [];
      byParent[key].push(node);
    }
    const out: ClusterGroupTreeNode[] = [];
    const walk = (parent: string) => {
      const kids = (byParent[parent] || [])
        .slice()
        .sort((a, b) => a.name.localeCompare(b.name));
      for (const k of kids) {
        out.push(k);
        walk(k.id);
      }
    };
    walk("__root__");
    return out;
  }, [tree]);
  const columns: Column<ClusterGroupTreeNode>[] = [
    {
      key: "name",
      header: "Name",
      accessor: (group) => (
        <div
          className="flex items-center gap-2"
          style={{ paddingLeft: `${group.depth * 16}px` }}
        >
          <span
            className="inline-flex h-5 w-5 items-center justify-center rounded"
            style={{ background: group.color + "33", color: group.color }}
            aria-label={group.icon}
          >
            <Folder className="h-3 w-3" />
          </span>
          <span className="font-medium text-foreground">{group.name}</span>
        </div>
      ),
      sortAccessor: (group) => group.name,
    },
    {
      key: "slug",
      header: "Slug",
      accessor: (group) => (
        <span className="font-mono text-xs text-muted-foreground">
          {group.slug}
        </span>
      ),
    },
    {
      key: "clusters",
      header: "Clusters",
      accessor: (group) => group.clusterCount,
      sortAccessor: (group) => group.clusterCount,
    },
    {
      key: "subtree",
      header: "Subtree",
      accessor: (group) => group.clusterCountTree,
      sortAccessor: (group) => group.clusterCountTree,
    },
    {
      key: "actions",
      header: "Actions",
      accessor: (group) => (
        <div className="flex items-center justify-end gap-1">
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              setEditing(group);
              setShowForm(true);
            }}
            className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            title="Edit"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={(event) => {
              event.stopPropagation();
              if (
                confirm(
                  `Delete "${group.name}"? This will remove the entire subtree. Clusters in the deleted tree will be unassigned (not deleted).`,
                )
              ) {
                deleteMut.mutate(group.id);
                if (selected?.id === group.id) setSelected(null);
              }
            }}
            className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-status-error/10 hover:text-status-error"
            title="Delete"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
    },
  ];

  return (
    <PageShell>
      <PageHeader
        title="Cluster groups"
        description={`Organize your clusters into folders — group by environment, region, or business unit. Tree depth is capped at ${MAX_DEPTH + 1} levels.`}
        actions={
          <ActionButton
            type="button"
            intent="primary"
            icon={<Plus className="h-4 w-4" />}
            onClick={() => {
              setEditing(null);
              setShowForm(true);
            }}
          >
            New group
          </ActionButton>
        }
      />

      <DataTable
        data={flattened}
        columns={columns}
        keyExtractor={(group) => group.id}
        loading={isLoading}
        emptyMessage="No cluster groups yet — create one to start organizing your clusters."
        onRowClick={setSelected}
      />

      {selected && <ClusterGroupDetails group={selected} />}

      {showForm && (
        <ClusterGroupForm
          existing={editing}
          allGroups={tree}
          onClose={() => {
            setShowForm(false);
            setEditing(null);
          }}
          onSubmit={(body) => {
            const action = editing
              ? updateMut.mutateAsync({ id: editing.id, body })
              : createMut.mutateAsync(body);
            action.then(() => {
              setShowForm(false);
              setEditing(null);
            });
          }}
        />
      )}
    </PageShell>
  );
}

function ClusterGroupDetails({ group }: { group: ClusterGroupTreeNode }) {
  const [assigning, setAssigning] = useState(false);
  const members = useQuery({
    queryKey: queryKeys.clusterGroups.members(group.id),
    queryFn: ({ signal }) => listClustersInGroup(group.id, { signal }),
  });
  const columns: Column<{ id: string; name: string }>[] = [
    {
      key: "name",
      header: "Cluster",
      accessor: (cluster) => (
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium">{cluster.name}</span>
        </div>
      ),
      sortAccessor: (cluster) => cluster.name,
    },
    {
      key: "id",
      header: "Cluster ID",
      accessor: (cluster) => (
        <span className="font-mono text-xs text-muted-foreground">
          {cluster.id}
        </span>
      ),
    },
  ];
  return (
    <PageSection
      title={group.name}
      description={
        group.description ||
        "Direct membership for this group. Subtree totals include child groups."
      }
      actions={
        <ActionButton
          type="button"
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => setAssigning(true)}
        >
          Assign clusters
        </ActionButton>
      }
    >
      <div className="mb-3 flex flex-wrap gap-4 text-sm text-muted-foreground">
        <span>{group.clusterCount} direct members</span>
        <span>{group.clusterCountTree} members in subtree</span>
        <span className="font-mono">{group.slug}</span>
      </div>
      <DataTable
        data={members.data ?? []}
        columns={columns}
        keyExtractor={(cluster) => cluster.id}
        loading={members.isLoading}
        isError={members.isError}
        onRetry={() => void members.refetch()}
        emptyMessage="No clusters are directly assigned to this group"
      />
      {assigning && (
        <ClusterMembershipDialog
          group={group}
          memberIds={new Set((members.data ?? []).map((member) => member.id))}
          onClose={() => setAssigning(false)}
        />
      )}
    </PageSection>
  );
}

function ClusterMembershipDialog({
  group,
  memberIds,
  onClose,
}: {
  group: ClusterGroupTreeNode;
  memberIds: Set<string>;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const clusters = useClusters({ pageSize: 200 });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const mutation = useMutation({
    mutationFn: () => moveClustersToGroup(group.id, [...selected]),
    onSuccess: (result) => {
      client.invalidateQueries({ queryKey: queryKeys.clusterGroups.all });
      client.invalidateQueries({
        queryKey: queryKeys.clusterGroups.members(group.id),
      });
      toastSuccess(
        `${result.moved} cluster${result.moved === 1 ? "" : "s"} assigned`,
      );
      onClose();
    },
    onError: (error: Error) =>
      toastApiError("Failed to assign clusters", error),
  });
  const available = (clusters.data?.data ?? []).filter(
    (cluster) => !memberIds.has(cluster.id),
  );
  return (
    <ModalShell
      title={`Assign clusters to ${group.name}`}
      subtitle="Moving a cluster updates its direct group membership. Rollout previews explain the resulting placement before launch."
      size="lg"
      onClose={onClose}
      footer={
        <div className="flex justify-end gap-2">
          <ActionButton type="button" intent="ghost" onClick={onClose}>
            Cancel
          </ActionButton>
          <ActionButton
            type="button"
            intent="primary"
            disabled={selected.size === 0 || mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            Assign {selected.size || "selected"}
          </ActionButton>
        </div>
      }
    >
      <div className="max-h-96 space-y-1 overflow-auto rounded-md border border-border p-2">
        {available.map((cluster) => (
          <label
            key={cluster.id}
            className="flex cursor-pointer items-center gap-3 rounded px-3 py-2 hover:bg-muted"
          >
            <input
              type="checkbox"
              checked={selected.has(cluster.id)}
              onChange={(event) => {
                setSelected((current) => {
                  const next = new Set(current);
                  if (event.target.checked) next.add(cluster.id);
                  else next.delete(cluster.id);
                  return next;
                });
              }}
            />
            <span className="font-medium">
              {cluster.displayName || cluster.name}
            </span>
            <span className="ml-auto font-mono text-xs text-muted-foreground">
              {cluster.id}
            </span>
          </label>
        ))}
        {!clusters.isLoading && available.length === 0 && (
          <p className="p-4 text-center text-sm text-muted-foreground">
            Every visible cluster is already assigned directly to this group.
          </p>
        )}
      </div>
    </ModalShell>
  );
}

interface FormProps {
  existing: ClusterGroupTreeNode | null;
  allGroups: ClusterGroupTreeNode[];
  onSubmit: (body: ClusterGroupWriteRequest) => void;
  onClose: () => void;
}

function ClusterGroupForm({
  existing,
  allGroups,
  onSubmit,
  onClose,
}: FormProps) {
  const [slugTouched, setSlugTouched] = useState(!!existing);

  const form = useAppForm({
    defaultValues: {
      name: existing?.name ?? "",
      slug: existing?.slug ?? "",
      description: existing?.description ?? "",
      parentId: existing?.parentId ?? "",
      color: existing?.color ?? CLUSTER_GROUP_COLORS[0],
      icon: existing?.icon ?? CLUSTER_GROUP_ICONS[0],
    },
    onSubmit: ({ value }) => {
      onSubmit({
        name: value.name,
        slug: value.slug,
        description: value.description,
        parent_id: value.parentId || undefined,
        color: value.color,
        icon: value.icon,
      });
    },
  });
  // Old disabled gate (`!name || !slug`), recomputed from form state.
  const name = useStore(form.store, (s) => s.values.name);
  const slug = useStore(form.store, (s) => s.values.slug);

  // Auto-derive slug from name unless the user typed one explicitly.
  const handleName = (v: string) => {
    form.setFieldValue("name", v);
    if (!slugTouched) {
      form.setFieldValue(
        "slug",
        v
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/^-+|-+$/g, ""),
      );
    }
  };

  // Parent options exclude self + descendants (no cycles) and any
  // candidate whose depth already pushes the new group past MAX_DEPTH.
  const parentOptions = useMemo(() => {
    const exclude = new Set<string>();
    if (existing) {
      exclude.add(existing.id);
      // Collect descendants.
      const stack = [existing.id];
      while (stack.length) {
        const cur = stack.pop()!;
        for (const g of allGroups) {
          if (g.parentId === cur) {
            exclude.add(g.id);
            stack.push(g.id);
          }
        }
      }
    }
    return allGroups
      .filter((g) => !exclude.has(g.id))
      .filter((g) => g.depth < MAX_DEPTH);
  }, [allGroups, existing]);

  return (
    <ModalShell
      title={existing ? "Edit cluster group" : "New cluster group"}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton type="button" intent="ghost" onClick={onClose}>
            Cancel
          </ActionButton>
          <ActionButton
            type="button"
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={!name || !slug}
          >
            {existing ? "Save" : "Create"}
          </ActionButton>
        </>
      }
    >
      <div className="space-y-3">
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Name
          </span>
          <form.Field name="name">
            {(field) => (
              <Input
                type="text"
                value={field.state.value}
                onChange={(e) => handleName(e.target.value)}
                onBlur={field.handleBlur}
                className="mt-1"
                data-initial-focus
              />
            )}
          </form.Field>
        </label>
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Slug{" "}
            <span className="text-muted-foreground/60">
              (URL-safe identifier)
            </span>
          </span>
          <form.Field name="slug">
            {(field) => (
              <Input
                type="text"
                value={field.state.value}
                onChange={(e) => {
                  field.handleChange(e.target.value);
                  setSlugTouched(true);
                }}
                onBlur={field.handleBlur}
                className="mt-1 font-mono"
              />
            )}
          </form.Field>
        </label>
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Description
          </span>
          <form.Field name="description">
            {(field) => (
              <Textarea
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                rows={2}
                className="mt-1 min-h-0"
              />
            )}
          </form.Field>
        </label>
        <label className="block">
          <span className="text-xs font-medium text-muted-foreground">
            Parent
          </span>
          <form.Field name="parentId">
            {(field) => (
              <Select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="mt-1"
              >
                <option value="">— Top-level —</option>
                {parentOptions.map((p) => (
                  <option key={p.id} value={p.id}>
                    {"— ".repeat(p.depth)}
                    {p.name}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </label>
        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">
              Color
            </span>
            <form.Field name="color">
              {(field) => (
                <div className="mt-1 flex flex-wrap gap-1">
                  {CLUSTER_GROUP_COLORS.map((c) => (
                    <button
                      type="button"
                      key={c}
                      onClick={() => field.handleChange(c)}
                      className="h-7 w-7 rounded border-2"
                      style={{
                        background: c,
                        borderColor:
                          field.state.value === c
                            ? "hsl(var(--primary-foreground))"
                            : "transparent",
                        outline:
                          field.state.value === c ? `2px solid ${c}` : "none",
                      }}
                      aria-label={`Color ${c}`}
                    />
                  ))}
                </div>
              )}
            </form.Field>
          </label>
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">
              Icon
            </span>
            <form.Field name="icon">
              {(field) => (
                <Select
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  className="mt-1"
                >
                  {CLUSTER_GROUP_ICONS.map((i) => (
                    <option key={i} value={i}>
                      {i}
                    </option>
                  ))}
                </Select>
              )}
            </form.Field>
          </label>
        </div>
      </div>
    </ModalShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/cluster-groups/")({
  component: ClusterGroupsPage,
});
