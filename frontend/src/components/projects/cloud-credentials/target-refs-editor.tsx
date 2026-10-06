/**
 * Multi-cluster / multi-namespace selector for cloud-credential target refs.
 *
 * The user picks one or more clusters; for each picked cluster they then
 * pick the namespaces where the rendered Secret should land. Both selections
 * come from the existing project list / per-cluster namespace endpoints so
 * we don't have to bake any project context into the parent.
 */
import { useState } from "react";
import { Plus, Trash2, ChevronDown, ChevronUp } from "lucide-react";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { useCluster, useClusterNamespaces } from "@/lib/hooks/clusters";
import { cn } from "@/lib/utils";
import type { CloudCredentialTargetRef } from "@/lib/api/project-detail";
import { LoadingSkeleton } from "@/components/form/loading-skeleton";
import { BareButton } from "@/components/form/bare-button";

interface TargetRefsEditorProps {
  value: CloudCredentialTargetRef[];
  onChange: (refs: CloudCredentialTargetRef[]) => void;
}

export function TargetRefsEditor({ value, onChange }: TargetRefsEditorProps) {
  const [pendingCluster, setPendingCluster] = useState("");

  const addCluster = () => {
    if (!pendingCluster) return;
    onChange([...value, { clusterId: pendingCluster, namespaces: [] }]);
    setPendingCluster("");
  };

  const removeCluster = (clusterId: string) =>
    onChange(value.filter((r) => r.clusterId !== clusterId));

  const updateNamespaces = (clusterId: string, namespaces: string[]) =>
    onChange(
      value.map((r) => (r.clusterId === clusterId ? { ...r, namespaces } : r)),
    );

  return (
    <div className="space-y-3">
      {value.length === 0 && (
        <p className="text-xs text-muted-foreground">No clusters added yet.</p>
      )}

      {value.map((ref) => (
        <ClusterRefRow
          key={ref.clusterId}
          ref_={ref}
          onRemove={() => removeCluster(ref.clusterId)}
          onNamespacesChange={(ns) => updateNamespaces(ref.clusterId, ns)}
        />
      ))}

      {/* Add cluster picker */}
      <div className="flex items-center gap-2">
        <RemoteClusterPicker
          ariaLabel="Cluster to add"
          value={pendingCluster}
          onChange={setPendingCluster}
          excludedClusterIds={value.map((ref) => ref.clusterId)}
          placeholder="Add a cluster…"
          className="flex-1"
        />
        <BareButton
          onClick={addCluster}
          disabled={!pendingCluster}
          className="inline-flex items-center gap-1 h-9 px-3 rounded-md border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" />
          Add
        </BareButton>
      </div>
    </div>
  );
}

function ClusterRefRow({
  ref_,
  onRemove,
  onNamespacesChange,
}: {
  ref_: CloudCredentialTargetRef;
  onRemove: () => void;
  onNamespacesChange: (ns: string[]) => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const { data: cluster } = useCluster(ref_.clusterId);
  const { data: namespaces, isLoading } = useClusterNamespaces(ref_.clusterId);
  const clusterDisplayName =
    cluster?.displayName || cluster?.name || ref_.clusterName || ref_.clusterId;

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
        <BareButton
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
            {ref_.namespaces.length} namespace
            {ref_.namespaces.length === 1 ? "" : "s"}
          </span>
        </BareButton>
        <BareButton
          aria-label="Remove cluster"
          onClick={onRemove}
          className="p-1.5 rounded-sm text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          tooltip="Remove cluster"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </BareButton>
      </div>

      {expanded && (
        <div className="border-t border-border px-3 py-2">
          {isLoading ? (
            <LoadingSkeleton label="Loading namespaces" lines={2} />
          ) : !namespaces || namespaces.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              No namespaces visible in this cluster.
            </p>
          ) : (
            <div className="flex flex-wrap gap-1.5 max-h-40 overflow-y-auto">
              {namespaces.map((ns) => {
                const selected = ref_.namespaces.includes(ns.name);
                return (
                  <BareButton
                    key={ns.name}
                    onClick={() => toggle(ns.name)}
                    className={cn(
                      "px-2.5 py-1 rounded-sm text-xs font-mono transition-colors",
                      selected
                        ? "bg-primary text-primary-foreground"
                        : "bg-muted text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {ns.name}
                  </BareButton>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
