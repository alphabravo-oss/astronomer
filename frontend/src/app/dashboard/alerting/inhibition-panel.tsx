'use client';

/**
 * Alertmanager-style inhibition rules panel (P-03) — rendered as the
 * "Inhibitions" tab of the alerting page.
 *
 * An enabled inhibition suppresses dispatch of a firing TARGET alert while a
 * SOURCE alert (matching source_matchers) is also firing and shares an equal
 * value on every label in equal_labels. Matchers mirror the silence UI's
 * label/value editor, extended with a per-matcher regex toggle to match the
 * P-03 contract.
 */
import { useState } from 'react';
import { Plus, X, Loader2, Trash2, Pencil } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { formatRelativeTime } from '@/lib/utils';
import type { AlertInhibition, InhibitionMatcher } from '@/types';
import { toInhibitionWriteRequest } from '@/lib/api/alerting-inhibitions';
import {
  useInhibitions,
  useCreateInhibition,
  useUpdateInhibition,
  useDeleteInhibition,
} from './inhibition-hooks';

function MatcherChips({ matchers }: { matchers: InhibitionMatcher[] }) {
  if (!matchers || matchers.length === 0) {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {matchers.map((m, i) => (
        <span
          key={`${m.label}-${i}`}
          className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
        >
          {m.label}
          {m.isRegex ? '=~' : '='}
          {m.value}
        </span>
      ))}
    </div>
  );
}

export function InhibitionPanel() {
  const { data, isLoading, isError, refetch } = useInhibitions();
  const del = useDeleteInhibition();

  const [showModal, setShowModal] = useState(false);
  const [editing, setEditing] = useState<AlertInhibition | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<AlertInhibition | null>(null);

  const columns: Column<AlertInhibition>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => <span className="font-medium text-foreground">{row.name}</span>,
    },
    {
      key: 'source',
      header: 'Source matchers',
      sortable: false,
      accessor: (row) => <MatcherChips matchers={row.sourceMatchers} />,
    },
    {
      key: 'target',
      header: 'Target matchers',
      sortable: false,
      accessor: (row) => <MatcherChips matchers={row.targetMatchers} />,
    },
    {
      key: 'equal',
      header: 'Equal labels',
      sortable: false,
      accessor: (row) =>
        row.equalLabels && row.equalLabels.length > 0 ? (
          <div className="flex flex-wrap gap-1">
            {row.equalLabels.map((l) => (
              <span key={l} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                {l}
              </span>
            ))}
          </div>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        ),
    },
    {
      key: 'enabled',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
          size="sm"
        />
      ),
      sortAccessor: (row) => (row.enabled ? '1' : '0'),
    },
    {
      key: 'updated',
      header: 'Updated',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.updatedAt)}</span>,
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => {
              setEditing(row);
              setShowModal(true);
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Edit inhibition"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => setDeleteTarget(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete inhibition"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end">
        <button
          onClick={() => {
            setEditing(null);
            setShowModal(true);
          }}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          Create Inhibition
        </button>
      </div>

      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        searchPlaceholder="Search inhibition rules..."
        emptyMessage="No inhibition rules configured"
      />

      {showModal && (
        <InhibitionModal
          inhibition={editing}
          onClose={() => {
            setShowModal(false);
            setEditing(null);
          }}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return;
          await del.mutateAsync(deleteTarget.id);
          setDeleteTarget(null);
        }}
        title="Delete inhibition rule?"
        description={`This removes "${deleteTarget?.name}". Alerts it was suppressing will dispatch again on the next eval cycle.`}
        confirmText="Delete"
        variant="destructive"
        loading={del.isPending}
      />
    </div>
  );
}

// ============================================================
// Create / edit modal
// ============================================================

interface DraftMatcher {
  label: string;
  value: string;
  isRegex: boolean;
}

function MatcherEditor({
  title,
  hint,
  matchers,
  onChange,
}: {
  title: string;
  hint: string;
  matchers: DraftMatcher[];
  onChange: (next: DraftMatcher[]) => void;
}) {
  const [draft, setDraft] = useState<DraftMatcher>({ label: '', value: '', isRegex: false });

  const add = () => {
    if (!draft.label || !draft.value) return;
    onChange([...matchers, draft]);
    setDraft({ label: '', value: '', isRegex: false });
  };
  const remove = (idx: number) => onChange(matchers.filter((_, i) => i !== idx));

  return (
    <div className="space-y-2">
      <div>
        <label className="text-sm font-medium text-foreground">{title}</label>
        <p className="text-2xs text-muted-foreground">{hint}</p>
      </div>
      <div className="flex gap-2">
        <input
          type="text"
          value={draft.label}
          onChange={(e) => setDraft((d) => ({ ...d, label: e.target.value }))}
          placeholder="Label name"
          className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
        />
        <input
          type="text"
          value={draft.value}
          onChange={(e) => setDraft((d) => ({ ...d, value: e.target.value }))}
          placeholder="Value"
          className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-xs font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
        />
        <button
          type="button"
          onClick={() => setDraft((d) => ({ ...d, isRegex: !d.isRegex }))}
          className={`h-8 px-2.5 rounded border text-xs font-mono transition-colors ${
            draft.isRegex
              ? 'border-primary bg-primary/10 text-primary'
              : 'border-border text-muted-foreground hover:text-foreground'
          }`}
          title="Treat value as a regular expression"
        >
          .*
        </button>
        <button
          type="button"
          onClick={add}
          disabled={!draft.label || !draft.value}
          className="h-8 px-2.5 rounded border border-border text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" />
        </button>
      </div>
      {matchers.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {matchers.map((m, i) => (
            <span
              key={`${m.label}-${i}`}
              className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
            >
              {m.label}
              {m.isRegex ? '=~' : '='}
              {m.value}
              <button type="button" onClick={() => remove(i)} className="hover:text-foreground">
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function InhibitionModal({
  inhibition,
  onClose,
}: {
  inhibition: AlertInhibition | null;
  onClose: () => void;
}) {
  const create = useCreateInhibition();
  const update = useUpdateInhibition();
  const isEdit = !!inhibition;

  const [name, setName] = useState(inhibition?.name ?? '');
  const [enabled, setEnabled] = useState(inhibition?.enabled ?? true);
  const [sourceMatchers, setSourceMatchers] = useState<DraftMatcher[]>(
    inhibition?.sourceMatchers ?? [],
  );
  const [targetMatchers, setTargetMatchers] = useState<DraftMatcher[]>(
    inhibition?.targetMatchers ?? [],
  );
  const [equalInput, setEqualInput] = useState((inhibition?.equalLabels ?? []).join(', '));

  const handleSave = async () => {
    const equalLabels = equalInput
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    const body = toInhibitionWriteRequest({
      name,
      enabled,
      sourceMatchers,
      targetMatchers,
      equalLabels,
    });
    try {
      if (inhibition) {
        await update.mutateAsync({ id: inhibition.id, body });
      } else {
        await create.mutateAsync(body);
      }
      onClose();
    } catch {
      /* mutation toasts on error */
    }
  };

  const isPending = create.isPending || update.isPending;
  const canSave = !!name && sourceMatchers.length > 0 && targetMatchers.length > 0;

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">
            {isEdit ? 'Edit Inhibition Rule' : 'Create Inhibition Rule'}
          </h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-5">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Suppress node alerts when cluster is down"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <MatcherEditor
            title="Source matchers"
            hint="A firing alert matching these is the SOURCE that suppresses targets."
            matchers={sourceMatchers}
            onChange={setSourceMatchers}
          />

          <MatcherEditor
            title="Target matchers"
            hint="Firing alerts matching these are SUPPRESSED while a source fires."
            matchers={targetMatchers}
            onChange={setTargetMatchers}
          />

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">
              Equal labels <span className="text-2xs text-muted-foreground font-normal">(comma-separated)</span>
            </label>
            <input
              type="text"
              value={equalInput}
              onChange={(e) => setEqualInput(e.target.value)}
              placeholder="cluster, namespace"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-2xs text-muted-foreground">
              Source and target must share the same value on every label listed here for suppression to apply.
            </p>
          </div>

          <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 rounded border-border"
            />
            Enabled
          </label>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={isPending || !canSave}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {isEdit ? 'Save Changes' : 'Create Inhibition'}
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}
