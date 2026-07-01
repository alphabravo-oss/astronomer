'use client';

/**
 * "Restore from this Backup" modal. Surfaced both from the runs table on
 * the overview page and from the run-detail page. The body posted to
 * `POST /backups/{id}/restore` accepts:
 *   - `included_namespaces`: a subset of the original namespace selectors
 *   - `namespace_mapping`:    rename map applied during restore
 *   - `restore_pvs`:          whether to restore PersistentVolumes
 *
 * We expose all three with sensible defaults (everything that was backed
 * up; no rename; restore PVs). Empty rename rows are filtered before send.
 */

import { useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { AlertTriangle, Loader2, Plus, X } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';
import { useB2CreateRestore } from './hooks';
import type { BackupRun } from '@/types';

interface RestoreModalProps {
  backup: BackupRun;
  onClose: () => void;
}

interface MappingRow {
  from: string;
  to: string;
}

export function RestoreModal({ backup, onClose }: RestoreModalProps) {
  const router = useRouter();
  const create = useB2CreateRestore();
  const sourceNamespaces = (backup.includedNamespaces ?? []) as string[];

  const [includedFilter, setIncludedFilter] = useState<string[]>(sourceNamespaces);
  const [mappingRows, setMappingRows] = useState<MappingRow[]>([]);
  const [restorePVs, setRestorePVs] = useState(true);
  const [confirmText, setConfirmText] = useState('');

  const confirmOK = confirmText === backup.name;
  // When the backup captured namespaces, at least one must stay selected.
  // An empty selection is an explicit "restore nothing" that we must NOT
  // silently widen to "restore everything" (see handleSubmit).
  const namespacesOK = sourceNamespaces.length === 0 || includedFilter.length > 0;

  const toggleNamespace = (ns: string) => {
    setIncludedFilter((prev) =>
      prev.includes(ns) ? prev.filter((n) => n !== ns) : [...prev, ns],
    );
  };

  const handleSubmit = async () => {
    // Guard: an empty namespace selection means "restore nothing", which we
    // refuse rather than collapse to `undefined` (= restore ALL namespaces).
    if (!namespacesOK) return;
    const namespaceMapping: Record<string, string> = {};
    for (const r of mappingRows) {
      const from = r.from.trim();
      const to = r.to.trim();
      if (from && to) namespaceMapping[from] = to;
    }
    try {
      const restore = await create.mutateAsync({
        backup_id: backup.id,
        // Only omit the filter (= restore every captured namespace) when the
        // FULL set is selected. A strict subset is sent verbatim; the empty
        // set is blocked above so it can never reach here as `undefined`.
        included_namespaces:
          includedFilter.length === sourceNamespaces.length ? undefined : includedFilter,
        namespace_mapping:
          Object.keys(namespaceMapping).length > 0 ? namespaceMapping : undefined,
        restore_pvs: restorePVs,
      });
      onClose();
      if (restore?.id) {
        router.push(`/dashboard/backups/restores/${restore.id}`);
      }
    } catch {
      /* error toast handled in hook */
    }
  };

  return (
    <ModalShell
      title="Restore from Backup"
      onClose={onClose}
      panelClassName="max-w-lg max-h-[85vh] bg-popover flex flex-col overflow-hidden"
      bodyClassName="flex-1 overflow-y-auto"
      footerClassName="bg-muted/30"
      titleIcon={<AlertTriangle className="h-5 w-5 text-status-warning" />}
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSubmit}
            disabled={!confirmOK || !namespacesOK || create.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-status-warning text-white
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Start Restore
          </button>
        </div>
      )}
    >
          <div className="rounded-lg border border-status-warning/20 bg-status-warning/5 p-4 space-y-1">
            <p className="text-sm text-foreground font-medium">{backup.name}</p>
            <p className="text-xs text-muted-foreground">
              Velero will recreate resources from this backup. Existing objects with
              the same names are skipped by default.
            </p>
          </div>

          {sourceNamespaces.length > 0 && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Restore namespaces</label>
              <p className="text-xs text-muted-foreground">
                Pick a subset of the namespaces captured in this backup, or leave all
                selected to restore everything.
              </p>
              <div className="flex flex-wrap gap-1.5">
                {sourceNamespaces.map((ns) => {
                  const on = includedFilter.includes(ns);
                  return (
                    <button
                      key={ns}
                      onClick={() => toggleNamespace(ns)}
                      type="button"
                      className={`text-xs px-2 py-1 rounded font-mono transition-colors ${
                        on
                          ? 'bg-primary text-primary-foreground'
                          : 'bg-muted text-muted-foreground hover:text-foreground'
                      }`}
                    >
                      {ns}
                    </button>
                  );
                })}
              </div>
              {!namespacesOK && (
                <p className="text-xs text-status-error">
                  Select at least one namespace to restore.
                </p>
              )}
            </div>
          )}

          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium text-foreground">Namespace mapping (optional)</label>
              <button
                onClick={() => setMappingRows((rs) => [...rs, { from: '', to: '' }])}
                type="button"
                className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground transition-colors"
              >
                <Plus className="h-3 w-3" />
                Add
              </button>
            </div>
            {mappingRows.length === 0 && (
              <p className="text-xs text-muted-foreground">
                Use this to restore namespace <span className="font-mono">prod</span> as
                <span className="font-mono"> prod-restored</span>, for example.
              </p>
            )}
            {mappingRows.map((row, i) => (
              <div key={i} className="flex items-center gap-2">
                <input
                  type="text"
                  value={row.from}
                  onChange={(e) =>
                    setMappingRows((rs) =>
                      rs.map((r, j) => (j === i ? { ...r, from: e.target.value } : r)),
                    )
                  }
                  placeholder="prod"
                  className="flex-1 h-8 px-3 rounded-md border border-border bg-background text-sm font-mono
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                />
                <span className="text-muted-foreground">→</span>
                <input
                  type="text"
                  value={row.to}
                  onChange={(e) =>
                    setMappingRows((rs) =>
                      rs.map((r, j) => (j === i ? { ...r, to: e.target.value } : r)),
                    )
                  }
                  placeholder="prod-restored"
                  className="flex-1 h-8 px-3 rounded-md border border-border bg-background text-sm font-mono
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                />
                <button
                  type="button"
                  onClick={() => setMappingRows((rs) => rs.filter((_, j) => j !== i))}
                  className="p-1 text-muted-foreground hover:text-status-error transition-colors"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>

          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={restorePVs}
              onChange={(e) => setRestorePVs(e.target.checked)}
              className="rounded border-border text-primary focus:ring-ring"
            />
            <span className="text-sm text-foreground">Restore PersistentVolumes</span>
          </label>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Type <span className="font-mono text-primary">{backup.name}</span> to confirm
            </label>
            <input
              type="text"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder={backup.name}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>
    </ModalShell>
  );
}
