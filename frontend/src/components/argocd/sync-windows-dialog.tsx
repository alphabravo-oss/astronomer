'use client';

import { useMemo, useState, type ReactNode } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { ModalShell } from '@/components/ui/modal-shell';
import { CalendarClock, Loader2, Plus, Trash2 } from 'lucide-react';

import { patchArgoProject } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import type { ArgoProject, ArgoProjectSyncWindow } from '@/types';

interface SyncWindowsDialogProps {
  instanceId: string;
  project: ArgoProject;
  onClose: () => void;
}

interface WindowDraft {
  id: string;
  kind: 'allow' | 'deny';
  schedule: string;
  duration: string;
  applications: string;
  namespaces: string;
  clusters: string;
  manualSync: boolean;
  syncOverrun: boolean;
  useAndOperator: boolean;
  timeZone: string;
  description: string;
}

function splitSelectors(value: string): string[] {
  return value
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter(Boolean);
}

function selectorsToText(values?: string[]): string {
  return (values ?? []).join(', ');
}

function draftFromWindow(window: ArgoProjectSyncWindow, index: number): WindowDraft {
  return {
    id: `${index}-${window.kind}-${window.schedule}`,
    kind: window.kind === 'allow' ? 'allow' : 'deny',
    schedule: window.schedule ?? '',
    duration: window.duration ?? '',
    applications: selectorsToText(window.applications),
    namespaces: selectorsToText(window.namespaces),
    clusters: selectorsToText(window.clusters),
    manualSync: !!window.manualSync,
    syncOverrun: !!window.syncOverrun,
    useAndOperator: !!window.useAndOperator,
    timeZone: window.timeZone || 'UTC',
    description: window.description ?? '',
  };
}

function newDraft(kind: 'allow' | 'deny'): WindowDraft {
  return {
    id: `${kind}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    kind,
    schedule: kind === 'allow' ? '0 9 * * 1-5' : '0 22 * * 1-5',
    duration: kind === 'allow' ? '8h' : '10h',
    applications: '*',
    namespaces: '',
    clusters: '*',
    manualSync: kind === 'deny',
    syncOverrun: kind === 'allow',
    useAndOperator: false,
    timeZone: 'UTC',
    description: '',
  };
}

function draftToWindow(draft: WindowDraft): ArgoProjectSyncWindow {
  const applications = splitSelectors(draft.applications);
  const namespaces = splitSelectors(draft.namespaces);
  const clusters = splitSelectors(draft.clusters);
  return {
    kind: draft.kind,
    schedule: draft.schedule.trim(),
    duration: draft.duration.trim(),
    applications: applications.length ? applications : undefined,
    namespaces: namespaces.length ? namespaces : undefined,
    clusters: clusters.length ? clusters : undefined,
    manualSync: draft.manualSync || undefined,
    syncOverrun: draft.syncOverrun || undefined,
    useAndOperator: draft.useAndOperator || undefined,
    timeZone: draft.timeZone.trim() || undefined,
    description: draft.description.trim() || undefined,
  };
}

function draftHasScope(draft: WindowDraft): boolean {
  return splitSelectors(draft.applications).length > 0 ||
    splitSelectors(draft.namespaces).length > 0 ||
    splitSelectors(draft.clusters).length > 0;
}

function draftIsValid(draft: WindowDraft): boolean {
  const scheduleParts = draft.schedule.trim().split(/\s+/).filter(Boolean);
  const scheduleOk = draft.schedule.trim().startsWith('@') || scheduleParts.length === 5 || scheduleParts.length === 6;
  return scheduleOk && !!draft.duration.trim() && draftHasScope(draft);
}

export function SyncWindowsDialog({ instanceId, project, onClose }: SyncWindowsDialogProps) {
  const queryClient = useQueryClient();
  const [drafts, setDrafts] = useState<WindowDraft[]>(
    (project.spec.syncWindows ?? []).map(draftFromWindow),
  );
  const invalidCount = useMemo(() => drafts.filter((draft) => !draftIsValid(draft)).length, [drafts]);

  const updateDraft = (id: string, patch: Partial<WindowDraft>) => {
    setDrafts((current) => current.map((draft) => (draft.id === id ? { ...draft, ...patch } : draft)));
  };

  const save = useMutation({
    mutationFn: () =>
      patchArgoProject(instanceId, project.metadata.name, {
        syncWindows: drafts.map(draftToWindow),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.argocd.projects(instanceId) });
      toastSuccess('Sync windows saved');
      onClose();
    },
    onError: (err: Error) => toastApiError('Save failed', err),
  });

  return (
    <ModalShell
      title="Sync windows"
      onClose={onClose}
      size="xl"
      panelClassName="max-w-4xl max-h-[88vh] bg-popover overflow-hidden flex flex-col"
      bodyClassName="p-0 space-y-0 overflow-hidden flex flex-col"
      footerClassName="bg-muted/30"
      titleIcon={(
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center shrink-0">
          <CalendarClock className="h-4 w-4 text-muted-foreground" />
        </div>
      )}
      headerActions={<p className="text-xs text-muted-foreground font-mono truncate">{project.metadata.name}</p>}
      footer={(
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            disabled={save.isPending}
            className="inline-flex items-center h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent"
          >
            Cancel
          </button>
          <button
            onClick={() => save.mutate()}
            disabled={save.isPending || invalidCount > 0}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {save.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Save
          </button>
        </div>
      )}
    >
        <div className="flex items-center justify-between gap-3 px-6 py-3 border-b border-border bg-muted/20">
          <div className="flex items-center gap-2">
            <button
              onClick={() => setDrafts((current) => [...current, newDraft('allow')])}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border bg-background text-sm hover:bg-accent"
            >
              <Plus className="h-3.5 w-3.5" />
              Allow
            </button>
            <button
              onClick={() => setDrafts((current) => [...current, newDraft('deny')])}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border bg-background text-sm hover:bg-accent"
            >
              <Plus className="h-3.5 w-3.5" />
              Deny
            </button>
          </div>
          {invalidCount > 0 ? (
            <span className="text-xs text-destructive">{invalidCount} invalid</span>
          ) : (
            <span className="text-xs text-muted-foreground tabular-nums">{drafts.length} configured</span>
          )}
        </div>

        <div className="p-6 space-y-4 overflow-y-auto">
          {drafts.length === 0 ? (
            <div className="rounded-lg border border-dashed border-border p-8 text-center">
              <CalendarClock className="mx-auto h-6 w-6 text-muted-foreground" />
              <p className="mt-2 text-sm text-muted-foreground">No sync windows configured.</p>
            </div>
          ) : null}

          {drafts.map((draft, index) => (
            <div key={draft.id} className="rounded-lg border border-border bg-background p-4 space-y-4">
              <div className="flex items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                  <span className="text-xs text-muted-foreground tabular-nums">#{index + 1}</span>
                  <select
                    value={draft.kind}
                    onChange={(e) => updateDraft(draft.id, { kind: e.target.value as 'allow' | 'deny' })}
                    className="h-8 rounded-md border border-border bg-background px-2 text-sm"
                  >
                    <option value="allow">Allow</option>
                    <option value="deny">Deny</option>
                  </select>
                </div>
                <button
                  onClick={() => setDrafts((current) => current.filter((item) => item.id !== draft.id))}
                  className="inline-flex items-center justify-center h-8 w-8 rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                  title="Remove"
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <Field label="Schedule" className="md:col-span-2">
                  <input
                    value={draft.schedule}
                    onChange={(e) => updateDraft(draft.id, { schedule: e.target.value })}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    placeholder="0 22 * * 1-5"
                  />
                </Field>
                <Field label="Duration">
                  <input
                    value={draft.duration}
                    onChange={(e) => updateDraft(draft.id, { duration: e.target.value })}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    placeholder="1h"
                  />
                </Field>
                <Field label="Timezone">
                  <input
                    value={draft.timeZone}
                    onChange={(e) => updateDraft(draft.id, { timeZone: e.target.value })}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    placeholder="UTC"
                  />
                </Field>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                <Field label="Applications">
                  <input
                    value={draft.applications}
                    onChange={(e) => updateDraft(draft.id, { applications: e.target.value })}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    placeholder="*-prod"
                  />
                </Field>
                <Field label="Namespaces">
                  <input
                    value={draft.namespaces}
                    onChange={(e) => updateDraft(draft.id, { namespaces: e.target.value })}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    placeholder="production"
                  />
                </Field>
                <Field label="Clusters">
                  <input
                    value={draft.clusters}
                    onChange={(e) => updateDraft(draft.id, { clusters: e.target.value })}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono"
                    placeholder="prod-*"
                  />
                </Field>
              </div>

              <Field label="Description">
                <input
                  value={draft.description}
                  onChange={(e) => updateDraft(draft.id, { description: e.target.value })}
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm"
                  placeholder="Change freeze"
                />
              </Field>

              <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
                <Checkbox
                  checked={draft.manualSync}
                  onChange={(manualSync) => updateDraft(draft.id, { manualSync })}
                  label="Manual override"
                />
                <Checkbox
                  checked={draft.syncOverrun}
                  onChange={(syncOverrun) => updateDraft(draft.id, { syncOverrun })}
                  label="Sync overrun"
                />
                <Checkbox
                  checked={draft.useAndOperator}
                  onChange={(useAndOperator) => updateDraft(draft.id, { useAndOperator })}
                  label="AND matching"
                />
              </div>
            </div>
          ))}
        </div>
    </ModalShell>
  );
}

function Field({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <label className={`space-y-1.5 ${className ?? ''}`}>
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}

function Checkbox({ checked, onChange, label }: { checked: boolean; onChange: (checked: boolean) => void; label: string }) {
  return (
    <label className="inline-flex items-center gap-2 h-9 px-3 rounded-md border border-border bg-background text-sm">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="h-4 w-4 rounded border-border"
      />
      <span>{label}</span>
    </label>
  );
}
