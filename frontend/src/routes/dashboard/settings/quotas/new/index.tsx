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
  Save,
} from 'lucide-react';
import { toastError } from '@/lib/toast';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { PageHeader, PageShell } from '@/components/ui/page';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
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
      <Input
        type="number"
        value={value}
        min={0}
        onChange={(e) => onChange(Number(e.target.value))}
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
            <Input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="enterprise-tier"
              className="font-mono"
              autoFocus
            />
            <p className="text-xs text-muted-foreground">
              Lowercase, numbers, dashes. This becomes the immutable URL key.
            </p>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Display name</label>
            <Input
              type="text"
              value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })}
              placeholder="Enterprise"
            />
          </div>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Description</label>
          <Textarea
            value={form.description ?? ''}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={2}
            className="min-h-0 text-sm font-sans"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Enforcement</label>
          <Select
            value={form.enforcement}
            onChange={(e) => setForm({ ...form, enforcement: e.target.value as QuotaEnforcement })}
          >
            <option value="hard">Hard — reject writes over cap</option>
            <option value="soft">Soft — warn but allow</option>
            <option value="disabled">Disabled — record only</option>
          </Select>
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
        <ActionButton onClick={() => router.push('/dashboard/settings/quotas')}>
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={handleCreate}
          loading={create.isPending}
          icon={<Save className="h-3.5 w-3.5" />}
        >
          Create plan
        </ActionButton>
      </div>
    </div>
  );
}

function NewQuotaPlanPage() {
  return (
    <SettingsAuthGate>
      <PageShell className="max-w-3xl mx-auto">
        <Link
          href="/dashboard/settings/quotas"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to quotas
        </Link>
        <PageHeader
          eyebrow="Settings · Quotas · New"
          title={
            <span className="flex items-center gap-2">
              <Gauge className="h-5 w-5 text-muted-foreground" />
              New quota plan
            </span>
          }
        />
        <NewQuotaPlanForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/quotas/new/')({
  component: NewQuotaPlanPage,
});
