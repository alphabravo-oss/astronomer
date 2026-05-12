'use client';

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
import { useMemo, useState } from 'react';
import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { toast } from 'sonner';
import { Plus, Loader2, Trash2, Pencil, AlertCircle, Folder } from 'lucide-react';
import * as api from '@/lib/api';
import {
  CLUSTER_GROUP_COLORS,
  CLUSTER_GROUP_ICONS,
  type ClusterGroupTreeNode,
  type ClusterGroupWriteRequest,
} from '@/lib/api/cluster-groups';

const MAX_DEPTH = 2;

function useClusterGroups() {
  return useQuery({
    queryKey: ['cluster-groups'],
    queryFn: () => api.listClusterGroups(),
  });
}

function useCreateClusterGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ClusterGroupWriteRequest) => api.createClusterGroup(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['cluster-groups'] });
      toast.success('Cluster group created');
    },
    onError: (err: Error) => toast.error(`Failed to create: ${err.message}`),
  });
}

function useUpdateClusterGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: ClusterGroupWriteRequest }) =>
      api.updateClusterGroup(id, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['cluster-groups'] });
      toast.success('Cluster group updated');
    },
    onError: (err: Error) => toast.error(`Failed to update: ${err.message}`),
  });
}

function useDeleteClusterGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteClusterGroup(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['cluster-groups'] });
      toast.success('Cluster group deleted');
    },
    onError: (err: Error) => toast.error(`Failed to delete: ${err.message}`),
  });
}

export default function ClusterGroupsPage() {
  const { data, isLoading } = useClusterGroups();
  const createMut = useCreateClusterGroup();
  const updateMut = useUpdateClusterGroup();
  const deleteMut = useDeleteClusterGroup();

  const [editing, setEditing] = useState<ClusterGroupTreeNode | null>(null);
  const [showForm, setShowForm] = useState(false);

  const tree = data ?? [];

  // Sort the flat tree by parent → depth → name so siblings cluster
  // together visually. The server already returns rows ordered by
  // (depth, name) — we re-shape them into a parent-grouped view here.
  const flattened = useMemo(() => {
    const byParent: Record<string, ClusterGroupTreeNode[]> = {};
    for (const node of tree) {
      const key = node.parentId ?? '__root__';
      byParent[key] = byParent[key] || [];
      byParent[key].push(node);
    }
    const out: ClusterGroupTreeNode[] = [];
    const walk = (parent: string) => {
      const kids = (byParent[parent] || []).slice().sort((a, b) => a.name.localeCompare(b.name));
      for (const k of kids) {
        out.push(k);
        walk(k.id);
      }
    };
    walk('__root__');
    return out;
  }, [tree]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Cluster groups</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Organize your clusters into folders — group by environment, region, or business unit.
            Tree depth is capped at {MAX_DEPTH + 1} levels.
          </p>
        </div>
        <button
          type="button"
          onClick={() => {
            setEditing(null);
            setShowForm(true);
          }}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          New group
        </button>
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : flattened.length === 0 ? (
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/30 p-4 text-sm text-muted-foreground">
          <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
          <p>No cluster groups yet — create one to start organizing your fleet.</p>
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 border-b border-border">
              <tr>
                <th className="text-left px-4 py-2 font-medium text-muted-foreground">Name</th>
                <th className="text-left px-4 py-2 font-medium text-muted-foreground">Slug</th>
                <th className="text-right px-4 py-2 font-medium text-muted-foreground">Clusters</th>
                <th className="text-right px-4 py-2 font-medium text-muted-foreground">Subtree</th>
                <th className="px-4 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {flattened.map((g) => (
                <tr key={g.id} className="border-b border-border last:border-b-0">
                  <td className="px-4 py-2">
                    <div
                      className="flex items-center gap-2"
                      style={{ paddingLeft: `${g.depth * 16}px` }}
                    >
                      <span
                        className="inline-flex items-center justify-center h-5 w-5 rounded"
                        style={{ background: g.color + '33', color: g.color }}
                        aria-label={g.icon}
                      >
                        <Folder className="h-3 w-3" />
                      </span>
                      <span className="font-medium text-foreground">{g.name}</span>
                    </div>
                  </td>
                  <td className="px-4 py-2 text-xs font-mono text-muted-foreground">{g.slug}</td>
                  <td className="px-4 py-2 text-right tabular-nums">{g.clusterCount}</td>
                  <td className="px-4 py-2 text-right tabular-nums text-muted-foreground">
                    {g.clusterCountTree}
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex items-center gap-1 justify-end">
                      <button
                        type="button"
                        onClick={() => {
                          setEditing(g);
                          setShowForm(true);
                        }}
                        className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted transition-colors"
                        title="Edit"
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          if (
                            confirm(
                              `Delete "${g.name}"? This will remove the entire subtree. Clusters in the deleted tree will be unassigned (not deleted).`,
                            )
                          ) {
                            deleteMut.mutate(g.id);
                          }
                        }}
                        className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
                        title="Delete"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

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
    </div>
  );
}

interface FormProps {
  existing: ClusterGroupTreeNode | null;
  allGroups: ClusterGroupTreeNode[];
  onSubmit: (body: ClusterGroupWriteRequest) => void;
  onClose: () => void;
}

function ClusterGroupForm({ existing, allGroups, onSubmit, onClose }: FormProps) {
  const [name, setName] = useState(existing?.name ?? '');
  const [slug, setSlug] = useState(existing?.slug ?? '');
  const [slugTouched, setSlugTouched] = useState(!!existing);
  const [description, setDescription] = useState(existing?.description ?? '');
  const [parentId, setParentId] = useState(existing?.parentId ?? '');
  const [color, setColor] = useState(existing?.color ?? CLUSTER_GROUP_COLORS[0]);
  const [icon, setIcon] = useState(existing?.icon ?? CLUSTER_GROUP_ICONS[0]);

  // Auto-derive slug from name unless the user typed one explicitly.
  const handleName = (v: string) => {
    setName(v);
    if (!slugTouched) {
      setSlug(
        v
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, '-')
          .replace(/^-+|-+$/g, ''),
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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-background border border-border rounded-lg shadow-lg w-full max-w-lg p-6 space-y-4">
        <h2 className="text-lg font-semibold text-foreground">
          {existing ? 'Edit cluster group' : 'New cluster group'}
        </h2>

        <div className="space-y-3">
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">Name</span>
            <input
              type="text"
              value={name}
              onChange={(e) => handleName(e.target.value)}
              className="mt-1 w-full h-9 px-3 rounded-md border border-border bg-background text-sm"
              autoFocus
            />
          </label>
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">
              Slug <span className="text-muted-foreground/60">(URL-safe identifier)</span>
            </span>
            <input
              type="text"
              value={slug}
              onChange={(e) => {
                setSlug(e.target.value);
                setSlugTouched(true);
              }}
              className="mt-1 w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
            />
          </label>
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">Description</span>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={2}
              className="mt-1 w-full px-3 py-2 rounded-md border border-border bg-background text-sm"
            />
          </label>
          <label className="block">
            <span className="text-xs font-medium text-muted-foreground">Parent</span>
            <select
              value={parentId}
              onChange={(e) => setParentId(e.target.value)}
              className="mt-1 w-full h-9 px-3 rounded-md border border-border bg-background text-sm"
            >
              <option value="">— Top-level —</option>
              {parentOptions.map((p) => (
                <option key={p.id} value={p.id}>
                  {'— '.repeat(p.depth)}
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <div className="grid grid-cols-2 gap-3">
            <label className="block">
              <span className="text-xs font-medium text-muted-foreground">Color</span>
              <div className="mt-1 flex flex-wrap gap-1">
                {CLUSTER_GROUP_COLORS.map((c) => (
                  <button
                    type="button"
                    key={c}
                    onClick={() => setColor(c)}
                    className="h-7 w-7 rounded border-2"
                    style={{
                      background: c,
                      borderColor: color === c ? '#fff' : 'transparent',
                      outline: color === c ? `2px solid ${c}` : 'none',
                    }}
                    aria-label={`Color ${c}`}
                  />
                ))}
              </div>
            </label>
            <label className="block">
              <span className="text-xs font-medium text-muted-foreground">Icon</span>
              <select
                value={icon}
                onChange={(e) => setIcon(e.target.value)}
                className="mt-1 w-full h-9 px-3 rounded-md border border-border bg-background text-sm"
              >
                {CLUSTER_GROUP_ICONS.map((i) => (
                  <option key={i} value={i}>
                    {i}
                  </option>
                ))}
              </select>
            </label>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="h-9 px-4 rounded-md text-sm text-muted-foreground hover:text-foreground"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() =>
              onSubmit({
                name,
                slug,
                description,
                parent_id: parentId || undefined,
                color,
                icon,
              })
            }
            disabled={!name || !slug}
            className="h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium disabled:opacity-50"
          >
            {existing ? 'Save' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  );
}
