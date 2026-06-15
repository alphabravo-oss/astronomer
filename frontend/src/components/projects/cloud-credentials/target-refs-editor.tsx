'use client';

/**
 * Multi-cluster / multi-namespace selector for cloud-credential target refs.
 *
 * The user picks one or more clusters; for each picked cluster they then
 * pick the namespaces where the rendered Secret should land. Both selections
 * come from the existing project list / per-cluster namespace endpoints so
 * we don't have to bake any project context into the parent.
 */
import { useMemo, useState } from 'react';
import { Plus, Trash2, ChevronDown, ChevronUp, Loader2 } from 'lucide-react';
import { useClusters, useClusterNamespaces } from '@/lib/hooks';
import { cn } from '@/lib/utils';
import type { CloudCredentialTargetRef } from '@/lib/api/project-detail';

interface TargetRefsEditorProps {
  value: CloudCredentialTargetRef[];
  onChange: (refs: CloudCredentialTargetRef[]) => void;
}

export function TargetRefsEditor({ value, onChange }: TargetRefsEditorProps) {
  const { data: clustersPage } = useClusters({ pageSize: 100 });
  const clusters = useMemo(() => clustersPage?.data ?? [], [clustersPage]);

  // Used to drive the "add cluster" picker dropdown.
  const remainingClusters = useMemo(
    () => clusters.filter((c) => !value.some((r) => r.clusterId === c.id)),
    [clusters, value],
  );
  const [pendingCluster, setPendingCluster] = useState('');

  const addCluster = () => {
    if (!pendingCluster) return;
    onChange([...value, { clusterId: pendingCluster, namespaces: [] }]);
    setPendingCluster('');
  };

  const removeCluster = (clusterId: string) =>
    onChange(value.filter((r) => r.clusterId !== clusterId));

  const updateNamespaces = (clusterId: string, namespaces: string[]) =>
    onChange(value.map((r) => (r.clusterId === clusterId ? { ...r, namespaces } : r)));

  return (
    <div className="space-y-3">
      {value.length === 0 && (
        <p className="text-xs text-muted-foreground">No clusters added yet.</p>
      )}

      {value.map((ref) => {
        const cluster = clusters.find((c) => c.id === ref.clusterId);
        return (
          <ClusterRefRow
            key={ref.clusterId}
            ref_={ref}
            clusterDisplayName={cluster?.displayName || cluster?.name || ref.clusterName || ref.clusterId}
            onRemove={() => removeCluster(ref.clusterId)}
            onNamespacesChange={(ns) => updateNamespaces(ref.clusterId, ns)}
          />
        );
      })}

      {/* Add cluster picker */}
      <div className="flex items-center gap-2">
        <select
          value={pendingCluster}
          onChange={(e) => setPendingCluster(e.target.value)}
          className="flex-1 h-9 px-3 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Add a cluster…</option>
          {remainingClusters.map((c) => (
            <option key={c.id} value={c.id}>
              {c.displayName || c.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={addCluster}
          disabled={!pendingCluster}
          className="inline-flex items-center gap-1 h-9 px-3 rounded-md border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" />
          Add
        </button>
      </div>
    </div>
  );
}

function ClusterRefRow({
  ref_,
  clusterDisplayName,
  onRemove,
  onNamespacesChange,
}: {
  ref_: CloudCredentialTargetRef;
  clusterDisplayName: string;
  onRemove: () => void;
  onNamespacesChange: (ns: string[]) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const { data: namespaces, isLoading } = useClusterNamespaces(ref_.clusterId);

  const toggle = (ns: string) => {
    onNamespacesChange(
      ref_.namespaces.includes(ns)
        ? ref_.namespaces.filter((n) => n !== ns)
        : [...ref_.namespaces, ns],
    );
  };

  return (
    <div className="rounded-lg border border-border bg-background">
      <div className="flex items-center justify-between px-3 py-2">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="flex items-center gap-2 text-sm text-foreground hover:text-foreground"
        >
          {expanded ? (
            <ChevronUp className="h-3.5 w-3.5 text-muted-foreground" />
          ) : (
            <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
          )}
          <span className="font-medium">{clusterDisplayName}</span>
          <span className="text-xs text-muted-foreground">
            {ref_.namespaces.length} namespace{ref_.namespaces.length === 1 ? '' : 's'}
          </span>
        </button>
        <button
          type="button"
          onClick={onRemove}
          className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          title="Remove cluster"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>

      {expanded && (
        <div className="border-t border-border px-3 py-2">
          {isLoading ? (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading namespaces…
            </div>
          ) : !namespaces || namespaces.length === 0 ? (
            <p className="text-xs text-muted-foreground">No namespaces visible in this cluster.</p>
          ) : (
            <div className="flex flex-wrap gap-1.5 max-h-40 overflow-y-auto">
              {namespaces.map((ns) => {
                const selected = ref_.namespaces.includes(ns.name);
                return (
                  <button
                    type="button"
                    key={ns.name}
                    onClick={() => toggle(ns.name)}
                    className={cn(
                      'px-2.5 py-1 rounded text-xs font-mono transition-colors',
                      selected
                        ? 'bg-primary text-primary-foreground'
                        : 'bg-muted text-muted-foreground hover:text-foreground',
                    )}
                  >
                    {ns.name}
                  </button>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
