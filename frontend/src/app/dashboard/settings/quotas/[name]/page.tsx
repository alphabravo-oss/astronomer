'use client';

/**
 * /dashboard/settings/quotas/[name] — edit a single quota plan.
 *
 * The backend's write contract uses snake_case (matching the Go `json:"..."`
 * tags). We keep camelCase on the client for ergonomics and convert at the
 * boundary in `handleSave`. Enforcement is the only enum; everything else is
 * an integer cap.
 */
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import {
  ArrowLeft,
  Gauge,
  Loader2,
  Save,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  useDeleteQuotaPlan,
  useQuotaPlan,
  useUpdateQuotaPlan,
} from '@/components/settings/hooks';
import type {
  QuotaEnforcement,
  QuotaPlan,
  QuotaPlanWriteRequest,
} from '@/lib/api/settings';

function toWrite(form: QuotaPlan): QuotaPlanWriteRequest {
  return {
    name: form.name,
    display_name: form.displayName,
    description: form.description,
    enforcement: form.enforcement,
    max_projects: form.maxProjects,
    max_clusters: form.maxClusters,
    max_namespaces: form.maxNamespaces,
    max_users: form.maxUsers,
    max_storage_gb: form.maxStorageGb,
    max_cpu_cores: form.maxCpuCores,
    max_memory_gb: form.maxMemoryGb,
    max_backups_per_day: form.maxBackupsPerDay,
    max_api_tokens: form.maxApiTokens,
  };
}

function NumberField({
  label,
  value,
  onChange,
  hint,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  hint?: string;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">{label}</label>
      <input
        type="number"
        value={value}
        min={0}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
      />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function QuotaPlanForm({ initial }: { initial: QuotaPlan }) {
  const router = useRouter();
  const update = useUpdateQuotaPlan();
  const del = useDeleteQuotaPlan();
  const [form, setForm] = useState<QuotaPlan>(initial);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    setForm(initial);
  }, [initial]);

  const dirty = JSON.stringify(form) !== JSON.stringify(initial);

  const handleSave = async () => {
    try {
      await update.mutateAsync({ name: form.name, body: toWrite(form) });
    } catch {
      // toast handled
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <h2 className="text-base font-semibold text-foreground">Identification</h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={form.name}
              disabled
              className="w-full h-10 px-3 rounded-lg border border-border bg-muted text-sm font-mono text-muted-foreground"
            />
            <p className="text-xs text-muted-foreground">Plan name is immutable.</p>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Display name</label>
            <input
              type="text"
              value={form.displayName}
              onChange={(e) => setForm({ ...form, displayName: e.target.value })}
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            />
          </div>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Description</label>
          <textarea
            value={form.description ?? ''}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={2}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Enforcement</label>
          <select
            value={form.enforcement}
            onChange={(e) => setForm({ ...form, enforcement: e.target.value as QuotaEnforcement })}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-ring"
          >
            <option value="hard">Hard — reject writes over cap</option>
            <option value="soft">Soft — warn but allow</option>
            <option value="disabled">Disabled — record only</option>
          </select>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <h2 className="text-base font-semibold text-foreground">Limits</h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <NumberField label="Max projects" value={form.maxProjects} onChange={(v) => setForm({ ...form, maxProjects: v })} />
          <NumberField label="Max clusters" value={form.maxClusters} onChange={(v) => setForm({ ...form, maxClusters: v })} />
          <NumberField label="Max namespaces" value={form.maxNamespaces} onChange={(v) => setForm({ ...form, maxNamespaces: v })} />
          <NumberField label="Max users" value={form.maxUsers} onChange={(v) => setForm({ ...form, maxUsers: v })} />
          <NumberField label="Max storage (GiB)" value={form.maxStorageGb} onChange={(v) => setForm({ ...form, maxStorageGb: v })} />
          <NumberField label="Max CPU cores" value={form.maxCpuCores} onChange={(v) => setForm({ ...form, maxCpuCores: v })} />
          <NumberField label="Max memory (GiB)" value={form.maxMemoryGb} onChange={(v) => setForm({ ...form, maxMemoryGb: v })} />
          <NumberField label="Max backups / day" value={form.maxBackupsPerDay} onChange={(v) => setForm({ ...form, maxBackupsPerDay: v })} />
          <NumberField label="Max API tokens" value={form.maxApiTokens} onChange={(v) => setForm({ ...form, maxApiTokens: v })} />
        </div>
        <p className="text-xs text-muted-foreground">
          Use <span className="font-mono">0</span> to mean unlimited.
        </p>
      </div>

      <div className="flex items-center justify-between sticky bottom-4 z-10 rounded-xl border border-border bg-popover/80 backdrop-blur p-3 shadow-sm">
        <button
          type="button"
          onClick={() => setConfirmDelete(true)}
          className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm font-medium text-status-error hover:bg-status-error/10 transition-colors"
        >
          <Trash2 className="h-3.5 w-3.5" />
          Delete plan
        </button>
        <div className="flex items-center gap-3">
          <p className="text-xs text-muted-foreground">{dirty ? 'Unsaved changes' : 'Saved'}</p>
          <button
            type="button"
            onClick={handleSave}
            disabled={!dirty || update.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {update.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            Save changes
          </button>
        </div>
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={async () => {
          await del.mutateAsync(form.name);
          router.push('/dashboard/settings/quotas');
        }}
        title="Delete quota plan?"
        description={`Deleting "${form.displayName}" only works if no tenant is currently bound to this plan.`}
        confirmText="Delete"
        variant="destructive"
      />
    </div>
  );
}

function QuotaPlanInner() {
  const params = useParams<{ name: string }>();
  const name = params?.name ? decodeURIComponent(params.name) : undefined;
  const { data, isLoading, error } = useQuotaPlan(name);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (error || !data) {
    toast.error('Failed to load quota plan');
    return (
      <div className="rounded-xl border border-border bg-card p-6">
        <p className="text-sm text-status-error">Quota plan not found.</p>
      </div>
    );
  }
  return <QuotaPlanForm initial={data} />;
}

export default function QuotaPlanDetailPage() {
  return (
    <SettingsAuthGate>
      <div className="max-w-3xl mx-auto space-y-6">
        <Link
          href="/dashboard/settings/quotas"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to quotas
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Quota plan</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <Gauge className="h-5 w-5 text-muted-foreground" />
            Edit plan
          </h1>
        </div>
        <QuotaPlanInner />
      </div>
    </SettingsAuthGate>
  );
}
