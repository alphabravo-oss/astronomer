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
import { useAppForm, useStore } from '@/lib/form';
import { Plus, X, Trash2, Pencil } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { formatRelativeTime } from '@/lib/utils';
import type { AlertInhibition, InhibitionMatcher } from '@/types';
import { toInhibitionWriteRequest } from '@/lib/api/alerting-inhibitions';
import {
  useInhibitions,
  useCreateInhibition,
  useUpdateInhibition,
  useDeleteInhibition,
} from './-inhibition-hooks';

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
          <ActionButton
            size="icon"
            intent="ghost"
            title="Edit inhibition"
            onClick={() => {
              setEditing(row);
              setShowModal(true);
            }}
            icon={<Pencil className="h-3.5 w-3.5" />}
          />
          <ActionButton
            size="icon"
            intent="ghost"
            title="Delete inhibition"
            onClick={() => setDeleteTarget(row)}
            icon={<Trash2 className="h-3.5 w-3.5" />}
            className="hover:text-status-error hover:bg-status-error/10"
          />
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end">
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => {
            setEditing(null);
            setShowModal(true);
          }}
        >
          Create Inhibition
        </ActionButton>
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
        <Input
          value={draft.label}
          onChange={(e) => setDraft((d) => ({ ...d, label: e.target.value }))}
          placeholder="Label name"
          className="h-8 flex-1 font-mono text-xs w-auto"
        />
        <Input
          value={draft.value}
          onChange={(e) => setDraft((d) => ({ ...d, value: e.target.value }))}
          placeholder="Value"
          className="h-8 flex-1 font-mono text-xs w-auto"
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
        <ActionButton
          type="button"
          size="icon"
          onClick={add}
          disabled={!draft.label || !draft.value}
          icon={<Plus className="h-3.5 w-3.5" />}
          title="Add matcher"
        />
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

  const form = useAppForm({
    defaultValues: {
      name: inhibition?.name ?? '',
      enabled: inhibition?.enabled ?? true,
      sourceMatchers: (inhibition?.sourceMatchers ?? []) as DraftMatcher[],
      targetMatchers: (inhibition?.targetMatchers ?? []) as DraftMatcher[],
      equalInput: (inhibition?.equalLabels ?? []).join(', '),
    },
    onSubmit: async ({ value }) => {
      const equalLabels = value.equalInput
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const body = toInhibitionWriteRequest({
        name: value.name,
        enabled: value.enabled,
        sourceMatchers: value.sourceMatchers,
        targetMatchers: value.targetMatchers,
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
    },
  });

  const name = useStore(form.store, (s) => s.values.name);
  const sourceMatchers = useStore(form.store, (s) => s.values.sourceMatchers);
  const targetMatchers = useStore(form.store, (s) => s.values.targetMatchers);

  const isPending = create.isPending || update.isPending;
  // Old disabled gate, recomputed from form state 1:1.
  const canSave = !!name && sourceMatchers.length > 0 && targetMatchers.length > 0;

  return (
    <ModalShell
      title={isEdit ? 'Edit Inhibition Rule' : 'Create Inhibition Rule'}
      onClose={onClose}
      size="md"
      bodyClassName="space-y-5"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={!canSave}
            loading={isPending}
          >
            {isEdit ? 'Save Changes' : 'Create Inhibition'}
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Name</label>
        <form.Field name="name">
          {(field) => (
            <Input
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Suppress node alerts when cluster is down"
            />
          )}
        </form.Field>
      </div>

      <MatcherEditor
        title="Source matchers"
        hint="A firing alert matching these is the SOURCE that suppresses targets."
        matchers={sourceMatchers}
        onChange={(next) => form.setFieldValue('sourceMatchers', next)}
      />

      <MatcherEditor
        title="Target matchers"
        hint="Firing alerts matching these are SUPPRESSED while a source fires."
        matchers={targetMatchers}
        onChange={(next) => form.setFieldValue('targetMatchers', next)}
      />

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">
          Equal labels <span className="text-2xs text-muted-foreground font-normal">(comma-separated)</span>
        </label>
        <form.Field name="equalInput">
          {(field) => (
            <Input
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="cluster, namespace"
              className="font-mono"
            />
          )}
        </form.Field>
        <p className="text-2xs text-muted-foreground">
          Source and target must share the same value on every label listed here for suppression to apply.
        </p>
      </div>

      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
        <form.Field name="enabled">
          {(field) => (
            <input
              type="checkbox"
              checked={field.state.value}
              onChange={(e) => field.handleChange(e.target.checked)}
              onBlur={field.handleBlur}
              className="h-4 w-4 rounded border-border"
            />
          )}
        </form.Field>
        Enabled
      </label>
    </ModalShell>
  );
}
