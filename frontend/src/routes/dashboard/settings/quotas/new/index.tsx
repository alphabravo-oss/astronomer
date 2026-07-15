import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/quotas/new — create a new quota plan.
 *
 * Shares the same field set as the detail page; only the name field is
 * editable on this surface (it becomes the immutable URL key once saved).
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import {
  ArrowLeft,
  Gauge,
  Loader2,
  Save,
} from 'lucide-react';
import { toastError } from '@/lib/toast';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { useCreateQuotaPlan } from '@/components/settings/hooks';
import type { QuotaEnforcement, QuotaPlanWriteRequest } from '@/lib/api/settings';

const DEFAULT_FORM: QuotaPlanWriteRequest = {
  name: '',
  display_name: '',
  description: '',
  enforcement: 'soft',
  max_projects: 10,
  max_clusters: 5,
  max_namespaces: 50,
  max_users: 25,
  max_storage_gb: 500,
  max_cpu_cores: 64,
  max_memory_gb: 256,
  max_backups_per_day: 24,
  max_api_tokens: 25,
};

function NumberField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
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
    </div>
  );
}

function NewQuotaPlanForm() {
  const router = useRouter();
  const create = useCreateQuotaPlan();
  const [form, setForm] = useState<QuotaPlanWriteRequest>(DEFAULT_FORM);

  const handleCreate = async () => {
    if (!form.name) {
      toastError('Plan name is required');
      return;
    }
    if (!/^[a-z0-9][a-z0-9-]*$/.test(form.name)) {
      toastError('Plan name must be lowercase letters, numbers, and dashes');
      return;
    }
    try {
      const created = await create.mutateAsync(form);
      router.push(`/dashboard/settings/quotas/${encodeURIComponent(created.name)}`);
    } catch {
      // mutation toasts
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
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="enterprise-tier"
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              autoFocus
            />
            <p className="text-xs text-muted-foreground">
              Lowercase, numbers, dashes. This becomes the immutable URL key.
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Display name</label>
            <input
              type="text"
              value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })}
              placeholder="Enterprise"
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
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
          <NumberField label="Max projects" value={form.max_projects} onChange={(v) => setForm({ ...form, max_projects: v })} />
          <NumberField label="Max clusters" value={form.max_clusters} onChange={(v) => setForm({ ...form, max_clusters: v })} />
          <NumberField label="Max namespaces" value={form.max_namespaces} onChange={(v) => setForm({ ...form, max_namespaces: v })} />
          <NumberField label="Max users" value={form.max_users} onChange={(v) => setForm({ ...form, max_users: v })} />
          <NumberField label="Max storage (GiB)" value={form.max_storage_gb} onChange={(v) => setForm({ ...form, max_storage_gb: v })} />
          <NumberField label="Max CPU cores" value={form.max_cpu_cores} onChange={(v) => setForm({ ...form, max_cpu_cores: v })} />
          <NumberField label="Max memory (GiB)" value={form.max_memory_gb} onChange={(v) => setForm({ ...form, max_memory_gb: v })} />
          <NumberField label="Max backups / day" value={form.max_backups_per_day} onChange={(v) => setForm({ ...form, max_backups_per_day: v })} />
          <NumberField label="Max API tokens" value={form.max_api_tokens} onChange={(v) => setForm({ ...form, max_api_tokens: v })} />
        </div>
      </div>

      <div className="flex items-center justify-end gap-2">
        <Link
          href="/dashboard/settings/quotas"
          className="h-9 px-4 inline-flex items-center rounded-lg border border-border text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          Cancel
        </Link>
        <button
          type="button"
          onClick={handleCreate}
          disabled={create.isPending}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {create.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
          Create plan
        </button>
      </div>
    </div>
  );
}

function NewQuotaPlanPage() {
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
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">Settings · Quotas · New</p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1 flex items-center gap-2">
            <Gauge className="h-5 w-5 text-muted-foreground" />
            New quota plan
          </h1>
        </div>
        <NewQuotaPlanForm />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/quotas/new/')({
  component: NewQuotaPlanPage,
});
