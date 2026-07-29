'use client';

/**
 * One monitoring-stack lifecycle surface: status, preview, install / upgrade /
 * replace / uninstall, and everything the operation tracker reports while the
 * Helm work runs. Driven entirely by a StackFamilySpec, so the per-cluster
 * stack, shared Thanos and shared Alertmanager are the same screen with
 * different fields — and, importantly, the same RBAC gating.
 *
 * RBAC. The six endpoints do NOT share one permission:
 *
 *   status, preview      monitoring:read
 *   install              monitoring:create   (per-cluster) / monitoring:update (shared)
 *   upgrade, replace     monitoring:update
 *   uninstall            monitoring:delete   (per-cluster) / monitoring:update (shared)
 *
 * ...and the per-cluster routes evaluate their grant at CLUSTER scope while the
 * shared ones are global. The panel takes four already-resolved decisions
 * rather than computing them, because only the caller knows which scope applies
 * — see the two pages for how they are built. Controls the caller would be 403d
 * on are ABSENT, not disabled: a button that always fails is worse than no
 * button. Preview is deliberately kept for read-only callers, because that is
 * exactly what the endpoint's own gate allows.
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  Download,
  FileCode2,
  RefreshCw,
  Replace as ReplaceIcon,
  Trash2,
  Upload,
} from 'lucide-react';

import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { PermissionState } from '@/components/ui/empty-state';
import { StatusBadge } from '@/components/ui/status-badge';
import { cn, formatRelativeTime } from '@/lib/utils';
import type { PermissionDecision } from '@/lib/permissions';
import type { MonitoringStackTarget } from '@/lib/api/monitoring-stack';
import { useMonitoringStackController } from '@/components/monitoring/hooks';
import { StackOperationPanel } from '@/components/monitoring/stack-operation-panel';
import { StackPreviewDialog } from '@/components/monitoring/stack-preview-dialog';
import {
  buildStackBody,
  missingRequiredFields,
  replaceTriggeringChanges,
  seedStackValues,
  stackIsInstalled,
  stackStatusLabel,
  stackStatusTone,
  type StackFamilySpec,
  type StackField,
  type StackFormValues,
} from '@/components/monitoring/stack-spec';

export interface StackLifecyclePermissions {
  /** monitoring:read — status + preview. */
  read: PermissionDecision;
  /** monitoring:create (per-cluster) or monitoring:update (shared). */
  install: PermissionDecision;
  /** monitoring:update — upgrade, replace, and retrying a failed operation. */
  update: PermissionDecision;
  /** monitoring:delete (per-cluster) or monitoring:update (shared). */
  uninstall: PermissionDecision;
}

export interface StackOption {
  id: string;
  label: string;
  hint?: string;
}

export interface StackLifecyclePanelProps {
  target: MonitoringStackTarget;
  spec: StackFamilySpec;
  permissions: StackLifecyclePermissions;
  /** Options for `cluster` fields. Supplied by the page, which owns the fetch. */
  clusterOptions?: StackOption[];
  /** Options for `storageConfig` fields (backup storage configs). */
  storageOptions?: StackOption[];
  /** Applied to fields the recorded state leaves empty, e.g. the local cluster. */
  seedOverrides?: Record<string, string>;
  className?: string;
}

const inputClass =
  'h-8 w-full rounded-md border border-border bg-background px-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring';

function denialReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

export function StackLifecyclePanel({
  target,
  spec,
  permissions,
  clusterOptions,
  storageOptions,
  seedOverrides,
  className,
}: StackLifecyclePanelProps) {
  const canRead = permissions.read.allowed;
  const controller = useMonitoringStackController(target, { enabled: canRead });
  const { status: statusQuery, tracker, preview } = controller;
  const status = statusQuery.data;
  const installed = stackIsInstalled(status);

  const [values, setValues] = useState<StackFormValues>(() => seedStackValues(spec, null));
  const [dirty, setDirty] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [confirmUninstall, setConfirmUninstall] = useState(false);
  const [confirmReplace, setConfirmReplace] = useState(false);
  const seedStampRef = useRef<string>('');

  // Re-seed from the recorded desired state, but never over an operator's
  // in-progress edits. Posting the full seeded spec back is what keeps an
  // upgrade from reading as a namespace/release CHANGE and 409ing — see the
  // note on StackFamilySpec.defaults.
  useEffect(() => {
    if (dirty) return;
    const seeded = seedStackValues(spec, status);
    for (const [key, value] of Object.entries(seedOverrides ?? {})) {
      if (!seeded[key]) seeded[key] = value;
    }
    const stamp = JSON.stringify(seeded);
    if (stamp === seedStampRef.current) return;
    seedStampRef.current = stamp;
    setValues(seeded);
  }, [spec, status, seedOverrides, dirty]);

  const setField = (name: string, value: string) => {
    setDirty(true);
    setValues((prev) => ({ ...prev, [name]: value }));
  };

  const missing = useMemo(() => missingRequiredFields(spec, values), [spec, values]);
  const pendingReplaceReasons = useMemo(
    () => replaceTriggeringChanges(spec, values, status),
    [spec, values, status],
  );

  const canInstall = permissions.install.allowed;
  const canUpdate = permissions.update.allowed;
  const canUninstall = permissions.uninstall.allowed;
  const canMutate = canInstall || canUpdate || canUninstall;

  const busyReason = controller.isBusy
    ? 'An operation for this stack is already in progress.'
    : undefined;
  const missingReason = missing.length
    ? `Set ${missing.map((field) => field.label).join(', ')} first.`
    : undefined;
  const blockReason = busyReason ?? missingReason;

  const run = async (verb: 'install' | 'upgrade' | 'replace' | 'uninstall') => {
    const op = await controller.run(verb, buildStackBody(spec, values));
    if (op) setDirty(false);
    return op;
  };

  const openPreview = () => {
    setPreviewOpen(true);
    preview.mutate(buildStackBody(spec, values));
  };

  if (!canRead) {
    return (
      <PermissionState
        title={`${spec.title} is not visible to you`}
        permission="monitoring:read"
        className={className}
      />
    );
  }

  const releaseLabel = status?.releaseName || spec.defaults.releaseName || spec.title;

  return (
    <section
      aria-label={spec.title}
      data-testid={`stack-panel-${spec.key}`}
      className={cn('rounded-lg border border-border bg-card', className)}
    >
      <header className="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-sm font-semibold text-foreground">{spec.title}</h2>
            <StatusBadge
              status={stackStatusTone(status?.status)}
              label={statusQuery.isLoading ? 'Loading' : stackStatusLabel(status?.status)}
              size="sm"
            />
          </div>
          <p className="mt-1 max-w-2xl text-xs text-muted-foreground">{spec.description}</p>
        </div>
        <ActionButton
          size="sm"
          intent="ghost"
          icon={<RefreshCw className="h-3.5 w-3.5" />}
          onClick={() => {
            void statusQuery.refetch();
            tracker.refresh();
          }}
        >
          Refresh
        </ActionButton>
      </header>

      <div className="space-y-4 p-4">
        {statusQuery.isError && (
          <p className="rounded-md border border-status-error/30 bg-status-error/10 px-3 py-2 text-xs text-status-error">
            Failed to read stack status: {(statusQuery.error as Error)?.message}
          </p>
        )}

        <StackSummary status={status} installed={installed} />

        {status?.drifted && (
          <div className="flex items-start gap-2 rounded-md border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>
              The live release has drifted from the recorded desired state
              {status.driftReasons?.length ? `: ${status.driftReasons.join(', ')}` : ''}. An upgrade
              re-applies the desired values.
            </span>
          </div>
        )}

        {/* Retry re-enqueues the row, which the backend gates on
            monitoring:update at the operation's own scope. */}
        <StackOperationPanel tracker={tracker} canRetry={canUpdate} />

        {controller.replaceRequired && (
          <div className="flex items-start justify-between gap-3 rounded-md border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
            <div className="min-w-0">
              <p className="font-medium">{controller.replaceRequired.message}</p>
              {controller.replaceRequired.replaceReasons.length > 0 && (
                <p className="mt-0.5">
                  Reasons: {controller.replaceRequired.replaceReasons.join(', ')}.
                </p>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-2">
              {canUpdate && (
                <ActionButton size="sm" onClick={() => setConfirmReplace(true)}>
                  Replace instead
                </ActionButton>
              )}
              <ActionButton size="sm" intent="ghost" onClick={controller.clearReplaceRequired}>
                Dismiss
              </ActionButton>
            </div>
          </div>
        )}

        {canMutate ? (
          <div className="space-y-3">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Desired configuration
            </h3>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {spec.fields.map((field) => (
                <StackFieldControl
                  key={field.name}
                  field={field}
                  value={values[field.name] ?? ''}
                  onChange={(next) => setField(field.name, next)}
                  clusterOptions={clusterOptions}
                  storageOptions={storageOptions}
                />
              ))}
            </div>
            {installed && pendingReplaceReasons.length > 0 && (
              <p className="text-xs text-status-warning">
                Changing {pendingReplaceReasons.join(', ')} cannot be applied in place — Upgrade will
                be rejected and Replace (uninstall + reinstall) is required.
              </p>
            )}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            You can view this stack and preview its rendered values, but not change it.{' '}
            {denialReason(permissions.update)}
          </p>
        )}

        <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3">
          {/* Preview is a monitoring:read affordance — deliberately offered to
              read-only callers, which is exactly what the endpoint allows. */}
          <ActionButton
            size="sm"
            icon={<FileCode2 className="h-3.5 w-3.5" />}
            onClick={openPreview}
            disabled={missing.length > 0}
            disabledReason={missingReason}
          >
            Preview
          </ActionButton>

          {!installed && canInstall && (
            <ActionButton
              size="sm"
              intent="primary"
              icon={<Download className="h-3.5 w-3.5" />}
              onClick={() => void run('install')}
              loading={controller.isEnqueuing}
              loadingLabel="Queueing"
              disabled={!!blockReason}
              disabledReason={blockReason}
            >
              Install
            </ActionButton>
          )}

          {installed && canUpdate && (
            <>
              <ActionButton
                size="sm"
                intent="primary"
                icon={<Upload className="h-3.5 w-3.5" />}
                onClick={() => void run('upgrade')}
                loading={controller.isEnqueuing}
                loadingLabel="Queueing"
                disabled={!!blockReason}
                disabledReason={blockReason}
              >
                Upgrade
              </ActionButton>
              <ActionButton
                size="sm"
                icon={<ReplaceIcon className="h-3.5 w-3.5" />}
                onClick={() => setConfirmReplace(true)}
                disabled={!!blockReason}
                disabledReason={blockReason}
              >
                Replace
              </ActionButton>
            </>
          )}

          {installed && canUninstall && (
            <ActionButton
              size="sm"
              intent="destructive"
              icon={<Trash2 className="h-3.5 w-3.5" />}
              onClick={() => setConfirmUninstall(true)}
              disabled={!!busyReason}
              disabledReason={busyReason}
              className="ml-auto"
            >
              Uninstall
            </ActionButton>
          )}
        </div>
      </div>

      <StackPreviewDialog
        open={previewOpen}
        onClose={() => setPreviewOpen(false)}
        title={spec.title}
        preview={preview.data ?? null}
        isLoading={preview.isPending}
        error={(preview.error as Error) ?? null}
        actions={
          preview.data && !preview.data.requiresReplace && !installed && canInstall ? (
            <ActionButton
              size="sm"
              intent="primary"
              disabled={!!blockReason}
              disabledReason={blockReason}
              onClick={async () => {
                const op = await run('install');
                if (op) setPreviewOpen(false);
              }}
            >
              Install these values
            </ActionButton>
          ) : preview.data && !preview.data.requiresReplace && installed && canUpdate ? (
            <ActionButton
              size="sm"
              intent="primary"
              disabled={!!blockReason}
              disabledReason={blockReason}
              onClick={async () => {
                const op = await run('upgrade');
                if (op) setPreviewOpen(false);
              }}
            >
              Apply as upgrade
            </ActionButton>
          ) : null
        }
      />

      {/* Replace is uninstall + install: the PersistentVolumeClaims and the
          Alertmanager silences go either way, so it carries the SAME
          type-the-release-name guard as Uninstall. It is also reachable from
          two places (the action row and the replace_required banner), which
          makes a one-click confirm easy to hit by accident. */}
      <ConfirmDialog
        open={confirmReplace}
        onClose={() => setConfirmReplace(false)}
        onConfirm={async () => {
          controller.clearReplaceRequired();
          const op = await run('replace');
          if (op) setConfirmReplace(false);
        }}
        title={`Replace ${spec.title}`}
        description={`Replace uninstalls the "${releaseLabel}" release${
          status?.namespace ? ` in namespace ${status.namespace}` : ''
        } and installs it again with the values above. It destroys ${spec.destroys}. Monitoring is unavailable while it runs.`}
        confirmText="Replace"
        confirmValue={releaseLabel}
        variant="destructive"
        loading={controller.isEnqueuing}
      />

      {/* Uninstall names the release, the namespace and what is destroyed, and
          requires the release name to be typed — the repo's confirmValue
          convention for irreversible deletes. */}
      <ConfirmDialog
        open={confirmUninstall}
        onClose={() => setConfirmUninstall(false)}
        onConfirm={async () => {
          const op = await run('uninstall');
          if (op) setConfirmUninstall(false);
        }}
        title={`Uninstall ${spec.title}`}
        description={`This deletes the Helm release "${releaseLabel}"${
          status?.namespace ? ` from namespace ${status.namespace}` : ''
        }. It destroys ${spec.destroys}. This cannot be undone.`}
        confirmText="Uninstall"
        confirmValue={releaseLabel}
        variant="destructive"
        loading={controller.isEnqueuing}
      />
    </section>
  );
}

// ─────────────────────────────────────────────────────────────────────

function StackSummary({
  status,
  installed,
}: {
  status: ReturnType<typeof useMonitoringStackController>['status']['data'];
  installed: boolean;
}) {
  if (!installed) {
    return (
      <p className="text-xs text-muted-foreground">
        No Helm release is recorded for this stack. Preview the values below, then install.
      </p>
    );
  }

  const observed = status?.observedRelease;
  const rows: Array<[string, React.ReactNode]> = [
    ['Namespace', status?.namespace || '—'],
    ['Release', status?.releaseName || '—'],
    ['Chart version', status?.chartVersion || '—'],
    [
      'Helm status',
      observed ? (
        <span className="inline-flex items-center gap-1.5">
          <StatusBadge status={observed.status} size="sm" />
          {/*
            no-release-history-or-revision-rollback-ui (separate, still-open
            audit item): the deployed revision below is the anchor a release
            HISTORY table and a revision picker would hang off — the backend
            already records lastObservedRevision and the reconciler rolls back
            to a prior revision itself. Not built here.
          */}
          {typeof observed.revision === 'number' && (
            <span className="text-xs text-muted-foreground">rev {observed.revision}</span>
          )}
        </span>
      ) : (
        '—'
      ),
    ],
    ['Pods', typeof status?.pods === 'number' ? String(status.pods) : '—'],
    [
      'Observed',
      observed?.observedAt ? formatRelativeTime(observed.observedAt) : '—',
    ],
  ];

  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3 lg:grid-cols-6">
      {rows.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <dt className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
            {label}
          </dt>
          <dd className="truncate text-xs text-foreground">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function StackFieldControl({
  field,
  value,
  onChange,
  clusterOptions,
  storageOptions,
}: {
  field: StackField;
  value: string;
  onChange: (next: string) => void;
  clusterOptions?: StackOption[];
  storageOptions?: StackOption[];
}) {
  const label = (
    <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
      {field.label}
      {field.required && <span className="text-status-error">*</span>}
      {field.replaceTrigger && (
        <span
          className="rounded bg-muted px-1 py-0.5 text-[9px] uppercase tracking-wide text-muted-foreground"
          title="Changing this needs a reinstall (Replace), not an in-place upgrade."
        >
          replace
        </span>
      )}
    </span>
  );

  if (field.kind === 'boolean') {
    return (
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          checked={value === 'true'}
          onChange={(event) => onChange(event.target.checked ? 'true' : 'false')}
          className="mt-0.5 h-4 w-4 rounded border-border"
          aria-label={field.label}
        />
        <span className="min-w-0">
          {label}
          {field.help && <span className="mt-0.5 block text-[11px] text-muted-foreground">{field.help}</span>}
        </span>
      </label>
    );
  }

  // A checkbox cannot express "I am not asking for either" — and for these
  // fields the form has no idea what the current setting is, because no status
  // endpoint returns them (SERVER_BLIND_FIELDS). The empty option is what keeps
  // the key OUT of the request body so the backend's own policy applies.
  if (field.kind === 'tristate') {
    return (
      <label className="block min-w-0">
        {label}
        <select
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className={cn(inputClass, 'mt-1')}
          aria-label={field.label}
        >
          <option value="">{field.unsetLabel ?? 'Use backend default'}</option>
          <option value="true">Enabled</option>
          <option value="false">Disabled</option>
        </select>
        {field.help && (
          <span className="mt-0.5 block text-[11px] text-muted-foreground">{field.help}</span>
        )}
      </label>
    );
  }

  const options =
    field.kind === 'cluster' ? clusterOptions : field.kind === 'storageConfig' ? storageOptions : undefined;

  return (
    <label className="block min-w-0">
      {label}
      {options && options.length > 0 ? (
        <select
          value={value}
          onChange={(event) => onChange(event.target.value)}
          className={cn(inputClass, 'mt-1')}
          aria-label={field.label}
        >
          <option value="">{field.required ? 'Select…' : 'None'}</option>
          {options.map((option) => (
            <option key={option.id} value={option.id}>
              {option.label}
            </option>
          ))}
        </select>
      ) : (
        <input
          type={field.kind === 'number' ? 'number' : 'text'}
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={field.placeholder}
          className={cn(inputClass, 'mt-1')}
          aria-label={field.label}
        />
      )}
      {field.help && <span className="mt-0.5 block text-[11px] text-muted-foreground">{field.help}</span>}
    </label>
  );
}
