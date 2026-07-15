'use client';

import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Loader2, Rocket } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';
import { useRouter } from '@/lib/navigation';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { getClusters, listClusterGroups } from '@/lib/api';
import {
  createFleetOperation,
  evaluateFleetSelector,
  selectorIsEmpty,
  FLEET_OPERATION_TYPES,
  type CreateFleetOperationRequest,
  type FleetOperationType,
  type FleetOnError,
  type FleetSelector,
  type FleetStrategy,
  type SelectorCandidate,
} from '@/lib/api/fleet-operations';
import { queryKeys } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';
import { SelectorBuilder } from './selector-builder';
import { OperationSpecFields } from './operation-spec-fields';

interface CreateFleetOperationDialogProps {
  onClose: () => void;
}

export function CreateFleetOperationDialog({ onClose }: CreateFleetOperationDialogProps) {
  const router = useRouter();
  const queryClient = useQueryClient();

  // Selector + spec stay controlled child-editor state (SelectorBuilder /
  // OperationSpecFields are list-editor components, not kit fields).
  const [selector, setSelector] = useState<FleetSelector>({});
  const [spec, setSpec] = useState<Record<string, unknown> | undefined>({});

  // Clusters + groups power label autocomplete, the group multi-select, and
  // the client-side match-count preview (no backend dry-run endpoint exists).
  const clustersQuery = useQuery({ queryKey: queryKeys.clusters.list(), queryFn: () => getClusters({ pageSize: 500 }) });
  const groupsQuery = useQuery({ queryKey: queryKeys.clusterGroups.all, queryFn: listClusterGroups });

  const clusters = useMemo(() => clustersQuery.data?.data ?? [], [clustersQuery.data]);
  const groups = groupsQuery.data ?? [];

  const labelSuggestions = useMemo(() => {
    const out: Record<string, Set<string>> = {};
    for (const c of clusters) {
      for (const [k, v] of Object.entries(c.labels ?? {})) {
        (out[k] ??= new Set()).add(v);
      }
    }
    return Object.fromEntries(Object.entries(out).map(([k, set]) => [k, Array.from(set).sort()]));
  }, [clusters]);

  // Preview evaluates label/expression matchers only — group membership is
  // resolved server-side, so a selector using matchGroupIDs is annotated.
  const usesGroups = (selector.matchGroupIDs?.length ?? 0) > 0;
  const previewSelector: FleetSelector = { ...selector, matchGroupIDs: undefined };
  const candidates: SelectorCandidate[] = clusters.map((c) => ({ labels: c.labels ?? {} }));
  const matchCount = selectorIsEmpty(previewSelector)
    ? 0
    : evaluateFleetSelector(previewSelector, candidates).length;

  const create = useMutation({
    mutationFn: () => {
      const value = form.state.values;
      const effectiveMax =
        value.strategy === 'sequential' ? 1 : Math.min(100, Math.max(1, value.maxConcurrent));
      const body: CreateFleetOperationRequest = {
        name: value.name.trim(),
        description: value.description.trim() || undefined,
        operation_type: value.operationType,
        selector,
        strategy: value.strategy,
        max_concurrent: effectiveMax,
        on_error: value.onError,
        respect_maintenance_windows: value.respectMaintenanceWindows,
      };
      if (value.operationType !== 'rotate_agent_token') {
        body.operation_spec = spec ?? {};
      }
      return createFleetOperation(body);
    },
    onSuccess: (op) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.fleetOperations.all });
      toastSuccess(`Fleet operation "${op.name}" created`);
      onClose();
      router.push(`/dashboard/fleet/${op.id}`);
    },
    onError: (error: Error) => toastApiError('Create failed', error),
  });

  const form = useAppForm({
    defaultValues: {
      name: '',
      description: '',
      operationType: 'tool_upgrade' as FleetOperationType,
      strategy: 'parallel' as FleetStrategy,
      maxConcurrent: 3,
      onError: 'abort' as FleetOnError,
      respectMaintenanceWindows: true,
    },
    onSubmit: () => create.mutate(),
  });

  const name = useStore(form.store, (s) => s.values.name);
  const operationType = useStore(form.store, (s) => s.values.operationType);
  const strategy = useStore(form.store, (s) => s.values.strategy);

  const isToolOp =
    operationType === 'tool_upgrade' ||
    operationType === 'tool_install' ||
    operationType === 'tool_uninstall';
  const isTemplateOp = operationType === 'apply_template';

  const specValid =
    (!isToolOp || Boolean((spec?.slug as string | undefined)?.trim())) &&
    (!isTemplateOp || Boolean((spec?.template_id as string | undefined)?.trim()));

  // Old disabled gate, recomputed from form state 1:1.
  const emptySelector = selectorIsEmpty(selector);
  const canSubmit = name.trim().length > 0 && !emptySelector && specValid;

  const inputClass =
    'h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-1 focus:ring-ring';

  return (
    <ModalShell
      title="New fleet operation"
      onClose={onClose}
      size="xl"
      panelClassName="max-w-3xl max-h-[90vh] bg-popover overflow-hidden flex flex-col"
      bodyClassName="overflow-y-auto"
      footerClassName="bg-muted/30"
      titleIcon={
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-muted">
          <Rocket className="h-4 w-4 text-muted-foreground" />
        </div>
      }
      footer={
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs text-muted-foreground">
            {emptySelector ? (
              <span className="text-status-warning">Selector must match at least one cluster.</span>
            ) : (
              <>
                {matchCount} cluster{matchCount === 1 ? '' : 's'} match
                {usesGroups ? ' label filters (groups evaluated server-side)' : ''}
              </>
            )}
          </span>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={create.isPending}
              className="inline-flex h-8 items-center rounded px-3 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => void form.handleSubmit()}
              disabled={!canSubmit || create.isPending}
              className="inline-flex h-8 items-center gap-1.5 rounded bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Create operation
            </button>
          </div>
        </div>
      }
    >
      <div className="space-y-5">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <form.Field name="name">
              {(field) => (
                <input
                  aria-label="name"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="Upgrade monitoring across prod"
                  className={inputClass}
                />
              )}
            </form.Field>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Operation type</label>
            <form.Field name="operationType">
              {(field) => (
                <select
                  aria-label="operation type"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value as FleetOperationType)}
                  onBlur={field.handleBlur}
                  className={inputClass}
                >
                  {FLEET_OPERATION_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>
                      {t.label}
                    </option>
                  ))}
                </select>
              )}
            </form.Field>
          </div>
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Description</label>
          <form.Field name="description">
            {(field) => (
              <input
                aria-label="description"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Optional context for the audit trail"
                className={inputClass}
              />
            )}
          </form.Field>
        </div>

        <div className="rounded-lg border border-border p-4">
          <OperationSpecFields operationType={operationType} onChange={setSpec} />
        </div>

        <div className="rounded-lg border border-border p-4">
          <p className="mb-3 text-sm font-medium text-foreground">Target selector</p>
          <SelectorBuilder onChange={setSelector} labelSuggestions={labelSuggestions} groups={groups} />
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Strategy</label>
            <form.Field name="strategy">
              {(field) => (
                <select
                  aria-label="strategy"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value as FleetStrategy)}
                  onBlur={field.handleBlur}
                  className={inputClass}
                >
                  <option value="parallel">Parallel</option>
                  <option value="sequential">Sequential</option>
                </select>
              )}
            </form.Field>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Max concurrent</label>
            <form.Field name="maxConcurrent">
              {(field) => (
                <input
                  aria-label="max concurrent"
                  type="number"
                  min={1}
                  max={100}
                  value={strategy === 'sequential' ? 1 : field.state.value}
                  disabled={strategy === 'sequential'}
                  onChange={(e) => field.handleChange(Number(e.target.value) || 1)}
                  onBlur={field.handleBlur}
                  className={`${inputClass} disabled:opacity-50`}
                />
              )}
            </form.Field>
            {strategy === 'sequential' && (
              <p className="text-xs text-muted-foreground">Sequential runs one cluster at a time.</p>
            )}
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">On error</label>
            <form.Field name="onError">
              {(field) => (
                <select
                  aria-label="on error"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value as FleetOnError)}
                  onBlur={field.handleBlur}
                  className={inputClass}
                >
                  <option value="abort">Abort remaining</option>
                  <option value="continue">Continue</option>
                </select>
              )}
            </form.Field>
          </div>
          <label className="flex items-center gap-2 self-end pb-2 text-sm text-foreground">
            <form.Field name="respectMaintenanceWindows">
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
            Respect maintenance windows
          </label>
        </div>
      </div>
    </ModalShell>
  );
}
