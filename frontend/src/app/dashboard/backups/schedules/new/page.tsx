'use client';

/**
 * Schedule wizard.
 *
 * Walks the admin through creating a Velero Schedule CR backed by an
 * existing storage location. Five steps:
 *
 *   1. Identity        — name + storage selector
 *   2. Cron            — preset list / custom expression with human preview
 *   3. Scope           — included / excluded namespaces (live from cluster)
 *   4. Retention       — TTL (days) + retention count
 *   5. Review          — final confirmation, then POST /backups/schedules/
 *
 * Namespaces are fetched live from the storage's target cluster via the
 * existing `useClusterNamespaces` hook, which proxies through the agent
 * tunnel — so the picker always reflects ground truth.
 */

import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Calendar, Check, Loader2 } from 'lucide-react';
import { useClusterNamespaces } from '@/lib/hooks';
import {
  useB2CreateSchedule,
  useB2StorageLocations,
} from '@/components/backups/hooks';
import { CRON_PRESETS, cronToHuman, isPlausibleCron } from '@/components/backups/cron';
import { cn } from '@/lib/utils';

const STEPS = ['Identity', 'Schedule', 'Scope', 'Retention', 'Review'] as const;
type Step = number;

interface FormState {
  name: string;
  storageId: string;
  cron: string;
  cronMode: 'preset' | 'custom';
  includedNamespaces: string[];
  excludedNamespaces: string[];
  ttlDays: number;
  retentionCount: number;
  enabled: boolean;
}

export default function ScheduleWizardPage() {
  const router = useRouter();
  const storageQ = useB2StorageLocations();
  const create = useB2CreateSchedule();

  const [step, setStep] = useState<Step>(0);
  const [form, setForm] = useState<FormState>({
    name: '',
    storageId: '',
    cron: CRON_PRESETS[2].value,
    cronMode: 'preset',
    includedNamespaces: [],
    excludedNamespaces: [],
    ttlDays: 30,
    retentionCount: 7,
    enabled: true,
  });

  const update = <K extends keyof FormState>(k: K, v: FormState[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  const storage = useMemo(
    () => storageQ.data?.data.find((s) => s.id === form.storageId),
    [storageQ.data, form.storageId],
  );

  // Default-pick the marked-default storage on first load.
  useEffect(() => {
    if (form.storageId || !storageQ.data) return;
    const def = storageQ.data.data.find((s) => s.isDefault) ?? storageQ.data.data[0];
    if (def) update('storageId', def.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [storageQ.data]);

  // Live namespace picker for the selected storage's cluster. The hook is
  // disabled until we know the cluster id.
  const namespacesQ = useClusterNamespaces(storage?.clusterId ?? '');

  const cronValid = isPlausibleCron(form.cron);
  const cronHuman = cronToHuman(form.cron);

  const stepValid = useMemo(() => {
    switch (step) {
      case 0:
        return form.name.trim().length > 0 && form.storageId.length > 0;
      case 1:
        return cronValid;
      case 2:
        return true;
      case 3:
        return form.ttlDays >= 0 && form.retentionCount >= 1;
      default:
        return true;
    }
  }, [step, form, cronValid]);

  const handleNext = async () => {
    if (step === STEPS.length - 1) {
      try {
        const created = await create.mutateAsync({
          name: form.name.trim(),
          storage_id: form.storageId,
          cluster_id: storage?.clusterId,
          cron_expression: form.cron,
          included_namespaces:
            form.includedNamespaces.length > 0 ? form.includedNamespaces : undefined,
          excluded_namespaces:
            form.excludedNamespaces.length > 0 ? form.excludedNamespaces : undefined,
          ttl: form.ttlDays > 0 ? `${form.ttlDays * 24}h` : undefined,
          retention_count: form.retentionCount,
          enabled: form.enabled,
        });
        if (created?.id) {
          router.push('/dashboard/backups?tab=schedules');
        }
      } catch {
        /* hook surfaces error toast */
      }
      return;
    }
    setStep((s) => (s + 1) as Step);
  };

  const handleBack = () => {
    if (step === 0) {
      router.push('/dashboard/backups');
      return;
    }
    setStep((s) => (s - 1) as Step);
  };

  const namespaceList = (namespacesQ.data ?? []).map((n) => n.name);

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <div>
        <button
          onClick={() => router.push('/dashboard/backups')}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors mb-2"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to backups
        </button>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Create Schedule</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Define a Velero Schedule CR that emits Backup CRs on a cron expression.
        </p>
      </div>

      {/* Step indicator */}
      <ol className="flex items-center gap-2">
        {STEPS.map((label, i) => {
          const done = i < step;
          const current = i === step;
          return (
            <li key={label} className="flex-1 flex items-center gap-2">
              <span
                className={cn(
                  'flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full text-xs font-medium border transition-colors',
                  done && 'bg-status-success/10 border-status-success text-status-success',
                  current && 'bg-primary text-primary-foreground border-primary',
                  !done && !current && 'bg-muted text-muted-foreground border-border',
                )}
              >
                {done ? <Check className="h-3.5 w-3.5" /> : i + 1}
              </span>
              <span
                className={cn(
                  'text-xs font-medium',
                  current ? 'text-foreground' : 'text-muted-foreground',
                )}
              >
                {label}
              </span>
              {i < STEPS.length - 1 && <span className="flex-1 h-px bg-border" />}
            </li>
          );
        })}
      </ol>

      <div className="rounded-xl border border-border bg-card p-6 animate-fade-in">
        {step === 0 && (
          <div className="space-y-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => update('name', e.target.value)}
                placeholder="daily-platform-backup"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Storage Location</label>
              {storageQ.isLoading ? (
                <p className="text-xs text-muted-foreground flex items-center gap-1.5">
                  <Loader2 className="h-3 w-3 animate-spin" /> Loading…
                </p>
              ) : (storageQ.data?.data ?? []).length === 0 ? (
                <p className="text-xs text-status-warning">
                  No storage locations exist yet. Add one before creating a schedule.
                </p>
              ) : (
                <select
                  value={form.storageId}
                  onChange={(e) => update('storageId', e.target.value)}
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                    focus:outline-none focus:ring-1 focus:ring-ring"
                >
                  <option value="">Select…</option>
                  {(storageQ.data?.data ?? []).map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name} ({s.bucket})
                    </option>
                  ))}
                </select>
              )}
            </div>
            <label className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={form.enabled}
                onChange={(e) => update('enabled', e.target.checked)}
                className="rounded border-border text-primary focus:ring-ring"
              />
              <span className="text-sm text-foreground">Enable schedule immediately</span>
            </label>
          </div>
        )}

        {step === 1 && (
          <div className="space-y-4">
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => update('cronMode', 'preset')}
                className={cn(
                  'h-7 px-3 rounded-md text-xs font-medium transition-colors',
                  form.cronMode === 'preset'
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-muted-foreground hover:text-foreground',
                )}
              >
                Preset
              </button>
              <button
                type="button"
                onClick={() => update('cronMode', 'custom')}
                className={cn(
                  'h-7 px-3 rounded-md text-xs font-medium transition-colors',
                  form.cronMode === 'custom'
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-muted-foreground hover:text-foreground',
                )}
              >
                Custom
              </button>
            </div>

            {form.cronMode === 'preset' ? (
              <select
                value={form.cron}
                onChange={(e) => update('cron', e.target.value)}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                {CRON_PRESETS.map((p) => (
                  <option key={p.value} value={p.value}>
                    {p.label}
                  </option>
                ))}
              </select>
            ) : (
              <input
                type="text"
                value={form.cron}
                onChange={(e) => update('cron', e.target.value)}
                placeholder="0 2 * * *"
                spellCheck={false}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            )}

            <div
              className={cn(
                'flex items-start gap-2 rounded-lg border p-3',
                cronValid
                  ? 'border-border bg-muted/40'
                  : 'border-status-warning/30 bg-status-warning/5',
              )}
            >
              <Calendar
                className={cn(
                  'h-4 w-4 mt-0.5 flex-shrink-0',
                  cronValid ? 'text-muted-foreground' : 'text-status-warning',
                )}
              />
              <div className="min-w-0 space-y-0.5">
                <p className={cn('text-sm', cronValid ? 'text-foreground' : 'text-status-warning')}>
                  {cronValid ? cronHuman : 'Cron expression looks invalid'}
                </p>
                <p className="text-xs text-muted-foreground font-mono">{form.cron}</p>
              </div>
            </div>
          </div>
        )}

        {step === 2 && (
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">
              Pick which namespaces Velero should snapshot. Leave both lists empty to
              capture every namespace on the cluster (Velero default).
            </p>
            <NamespacePicker
              title="Included namespaces"
              namespaces={namespaceList}
              selected={form.includedNamespaces}
              loading={namespacesQ.isLoading}
              onChange={(v) => update('includedNamespaces', v)}
              emptyText={
                storage?.clusterId
                  ? 'No namespaces returned from the cluster.'
                  : 'Pick a storage location with a cluster on step 1 to see namespaces.'
              }
            />
            <NamespacePicker
              title="Excluded namespaces"
              namespaces={namespaceList}
              selected={form.excludedNamespaces}
              loading={namespacesQ.isLoading}
              onChange={(v) => update('excludedNamespaces', v)}
              emptyText="Same as above."
            />
          </div>
        )}

        {step === 3 && (
          <div className="space-y-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">TTL (days)</label>
              <input
                type="number"
                min={0}
                max={3650}
                value={form.ttlDays}
                onChange={(e) => update('ttlDays', parseInt(e.target.value, 10) || 0)}
                className="w-32 h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <p className="text-xs text-muted-foreground">
                Velero deletes a backup once it exceeds this age. Set 0 to keep forever.
              </p>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Retention count</label>
              <input
                type="number"
                min={1}
                max={365}
                value={form.retentionCount}
                onChange={(e) => update('retentionCount', parseInt(e.target.value, 10) || 1)}
                className="w-32 h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <p className="text-xs text-muted-foreground">
                Astronomer prunes older runs once this many newer successful backups exist.
              </p>
            </div>
          </div>
        )}

        {step === 4 && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Review and create. The Schedule CR is applied to{' '}
              <span className="text-foreground font-medium">
                {storage?.bslName ?? storage?.name ?? 'the selected cluster'}
              </span>{' '}
              when you click <span className="text-foreground font-medium">Create</span>.
            </p>
            <dl className="grid grid-cols-2 gap-3 text-sm">
              <Summary k="Name" v={form.name} />
              <Summary k="Storage" v={storage?.name ?? '--'} />
              <Summary k="Cron" v={cronHuman} />
              <Summary k="Cron (raw)" v={form.cron} mono />
              <Summary
                k="Included namespaces"
                v={
                  form.includedNamespaces.length > 0
                    ? form.includedNamespaces.join(', ')
                    : 'all'
                }
                mono
              />
              <Summary
                k="Excluded namespaces"
                v={
                  form.excludedNamespaces.length > 0
                    ? form.excludedNamespaces.join(', ')
                    : 'none'
                }
                mono
              />
              <Summary k="TTL" v={form.ttlDays > 0 ? `${form.ttlDays} day(s)` : 'forever'} />
              <Summary k="Retention count" v={String(form.retentionCount)} />
              <Summary k="Enabled" v={form.enabled ? 'Yes' : 'No'} />
            </dl>
          </div>
        )}
      </div>

      <div className="flex items-center justify-between">
        <button
          onClick={handleBack}
          disabled={create.isPending}
          className="inline-flex items-center gap-1.5 h-9 px-4 rounded-lg border border-border text-sm font-medium
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          {step === 0 ? 'Cancel' : 'Back'}
        </button>
        <button
          onClick={handleNext}
          disabled={!stepValid || create.isPending}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {step === STEPS.length - 1 ? 'Create' : 'Continue'}
        </button>
      </div>
    </div>
  );
}

function NamespacePicker({
  title,
  namespaces,
  selected,
  onChange,
  loading,
  emptyText,
}: {
  title: string;
  namespaces: string[];
  selected: string[];
  onChange: (v: string[]) => void;
  loading?: boolean;
  emptyText?: string;
}) {
  const toggle = (ns: string) => {
    onChange(selected.includes(ns) ? selected.filter((n) => n !== ns) : [...selected, ns]);
  };
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between">
        <label className="text-sm font-medium text-foreground">{title}</label>
        {selected.length > 0 && (
          <button
            onClick={() => onChange([])}
            type="button"
            className="text-xs text-muted-foreground hover:text-foreground transition-colors"
          >
            Clear
          </button>
        )}
      </div>
      {loading ? (
        <p className="text-xs text-muted-foreground flex items-center gap-1.5">
          <Loader2 className="h-3 w-3 animate-spin" /> Loading namespaces…
        </p>
      ) : namespaces.length === 0 ? (
        <p className="text-xs text-muted-foreground">{emptyText}</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {namespaces.map((ns) => {
            const on = selected.includes(ns);
            return (
              <button
                key={ns}
                type="button"
                onClick={() => toggle(ns)}
                className={cn(
                  'text-xs px-2 py-1 rounded font-mono transition-colors',
                  on
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-muted-foreground hover:text-foreground',
                )}
              >
                {ns}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Summary({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <>
      <dt className="text-xs text-muted-foreground">{k}</dt>
      <dd className={cn('text-sm text-foreground break-all', mono && 'font-mono')}>{v}</dd>
    </>
  );
}
