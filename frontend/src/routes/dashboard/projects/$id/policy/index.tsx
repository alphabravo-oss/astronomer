import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Project · Policy tab.
 *
 * Three editable sections backed by `PATCH /projects/{id}/policy/`:
 *   1. Pod Security — Kubernetes PSA profile (privileged / baseline / restricted)
 *   2. Resource Quota — CPU / memory / pod caps; empty input means unlimited
 *   3. Network Policy — isolated / allow-same-project / none
 *
 * Plus a live ResourceQuota.status.used table at the bottom sourced from
 * `/projects/{id}/quota-usage/`.
 *
 * Edit access is gated on `projects:update` via `useCurrentUser`. When the
 * user lacks the role we render the same form in read-only mode (inputs
 * disabled, Save hidden) so non-admins can still inspect policy.
 */
import { useEffect, useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { Loader2, Save, AlertCircle, ExternalLink } from 'lucide-react';
import { useCurrentUser } from '@/lib/hooks';
import {
  useProjectPolicy,
  useUpdateProjectPolicy,
  useProjectQuotaUsage,
  canEditProject,
} from '@/components/projects/hooks';
import type {
  PodSecurityProfile,
  NetworkPolicyMode,
  ProjectPolicyPatch,
} from '@/lib/api/project-detail';
import { cn } from '@/lib/utils';


const psaOptions: { value: PodSecurityProfile; label: string; description: string }[] = [
  {
    value: 'privileged',
    label: 'Privileged',
    description:
      'Unrestricted. Allows known privilege escalations — only safe for trusted system workloads.',
  },
  {
    value: 'baseline',
    label: 'Baseline',
    description:
      'Minimally restrictive. Prevents known privilege escalations while remaining easy to adopt.',
  },
  {
    value: 'restricted',
    label: 'Restricted',
    description:
      'Heavily restricted. Enforces current pod-hardening best practices for application workloads.',
  },
];

const netpolOptions: { value: NetworkPolicyMode; label: string; description: string }[] = [
  {
    value: 'isolated',
    label: 'Isolated',
    description: 'Default-deny ingress to project namespaces; only explicit NetworkPolicies allow traffic.',
  },
  {
    value: 'allow-same-project',
    label: 'Allow same project',
    description: 'Allow pods within the project to talk freely; deny ingress from other namespaces.',
  },
  {
    value: 'none',
    label: 'None',
    description: 'No managed NetworkPolicies; rely on the cluster default (usually allow-all).',
  },
];

function PolicyPage() {
  const params = useParams();
  const id = params.id as string;
  const { data: user } = useCurrentUser();
  const canEdit = canEditProject(user);

  const { data: policy, isLoading } = useProjectPolicy(id);
  const { data: usage } = useProjectQuotaUsage(id);
  const updateMutation = useUpdateProjectPolicy(id);

  // Local controlled form state. We keep the inputs as strings so we can
  // distinguish "" (unlimited) from a real numeric value without juggling
  // null/undefined in every onChange.
  const [psa, setPsa] = useState<PodSecurityProfile>('baseline');
  const [cpu, setCpu] = useState('');
  const [memory, setMemory] = useState('');
  const [pods, setPods] = useState('');
  const [netpol, setNetpol] = useState<NetworkPolicyMode>('isolated');

  useEffect(() => {
    if (!policy) return;
    setPsa(policy.podSecurityProfile);
    setCpu(policy.resourceQuotaCpu ?? '');
    setMemory(policy.resourceQuotaMemory ?? '');
    setPods(policy.resourceQuotaPods != null ? String(policy.resourceQuotaPods) : '');
    setNetpol(policy.networkPolicyMode);
  }, [policy]);

  // Aggregate cluster-level usage to render in the resource-quota section
  // preview. One project may span multiple namespaces; we sum and present a
  // single "X/Y" alongside the input so the operator has a sanity check.
  const usageSummary = useMemo(() => {
    if (!usage) return null;
    const rows = usage.rows;
    return {
      cpuUsed: rows.reduce((a, r) => a + parseCpu(r.cpuUsed), 0),
      cpuLimit: rows.reduce((a, r) => a + parseCpu(r.cpuLimit), 0),
      memoryUsed: rows.reduce((a, r) => a + parseMemMiB(r.memoryUsed), 0),
      memoryLimit: rows.reduce((a, r) => a + parseMemMiB(r.memoryLimit), 0),
      podsUsed: rows.reduce((a, r) => a + (r.podsUsed || 0), 0),
      podsLimit: rows.reduce((a, r) => a + (r.podsLimit || 0), 0),
    };
  }, [usage]);

  const handleSave = () => {
    if (!canEdit) return;
    // Build the patch body. Empty quota inputs are sent as null so the
    // backend can drop the cap rather than leave it untouched.
    const patch: ProjectPolicyPatch = {
      podSecurityProfile: psa,
      networkPolicyMode: netpol,
      resourceQuotaCpu: cpu.trim() === '' ? null : cpu.trim(),
      resourceQuotaMemory: memory.trim() === '' ? null : memory.trim(),
      resourceQuotaPods: pods.trim() === '' ? null : Number(pods.trim()),
    };
    updateMutation.mutate(patch);
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-5xl">
      {!canEdit && (
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/30 p-3 text-xs text-muted-foreground">
          <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
          <p>
            You can view this project&apos;s policy but not change it. Editing requires the{' '}
            <span className="font-mono">projects:update</span> permission.
          </p>
        </div>
      )}

      {/* --- Pod Security --- */}
      <section className="rounded-xl border border-border bg-card p-5 space-y-4">
        <header>
          <h2 className="text-sm font-medium text-foreground">Pod Security</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Sets the Kubernetes Pod Security Standard enforced on every namespace in this project.{' '}
            <a
              href="https://kubernetes.io/docs/concepts/security/pod-security-standards/"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 underline-offset-2 hover:underline"
            >
              Kubernetes docs
              <ExternalLink className="h-3 w-3" />
            </a>
          </p>
        </header>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
          {psaOptions.map((opt) => (
            <button
              type="button"
              key={opt.value}
              disabled={!canEdit}
              onClick={() => setPsa(opt.value)}
              className={cn(
                'text-left p-3 rounded-lg border transition-colors',
                psa === opt.value
                  ? 'border-foreground/40 bg-accent/40'
                  : 'border-border bg-background hover:bg-accent/30',
                !canEdit && 'opacity-60 cursor-not-allowed hover:bg-background',
              )}
            >
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    'h-3.5 w-3.5 rounded-full border',
                    psa === opt.value ? 'border-foreground bg-foreground/80' : 'border-border',
                  )}
                />
                <span className="text-sm font-medium text-foreground">{opt.label}</span>
              </div>
              <p className="text-xs text-muted-foreground mt-1.5">{opt.description}</p>
            </button>
          ))}
        </div>
      </section>

      {/* --- Resource Quota --- */}
      <section className="rounded-xl border border-border bg-card p-5 space-y-4">
        <header>
          <h2 className="text-sm font-medium text-foreground">Resource Quota</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Applied as ResourceQuota objects in every project namespace. Leave a field empty for
            no limit.
          </p>
        </header>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <QuotaInput
            label="CPU limit"
            placeholder="e.g. 4 or 2000m"
            hint={
              usageSummary && usageSummary.cpuLimit > 0
                ? `In use: ${usageSummary.cpuUsed.toFixed(2)} / ${usageSummary.cpuLimit.toFixed(2)} cores`
                : undefined
            }
            value={cpu}
            disabled={!canEdit}
            onChange={setCpu}
          />
          <QuotaInput
            label="Memory limit"
            placeholder="e.g. 8Gi"
            hint={
              usageSummary && usageSummary.memoryLimit > 0
                ? `In use: ${formatMiB(usageSummary.memoryUsed)} / ${formatMiB(usageSummary.memoryLimit)}`
                : undefined
            }
            value={memory}
            disabled={!canEdit}
            onChange={setMemory}
          />
          <QuotaInput
            label="Pod count"
            placeholder="e.g. 50"
            hint={
              usageSummary && usageSummary.podsLimit > 0
                ? `In use: ${usageSummary.podsUsed} / ${usageSummary.podsLimit} pods`
                : undefined
            }
            value={pods}
            disabled={!canEdit}
            onChange={setPods}
          />
        </div>
      </section>

      {/* --- Network Policy --- */}
      <section className="rounded-xl border border-border bg-card p-5 space-y-4">
        <header>
          <h2 className="text-sm font-medium text-foreground">Network Policy</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Controls the default ingress posture managed for project namespaces.
          </p>
        </header>
        <div className="space-y-2">
          {netpolOptions.map((opt) => (
            <label
              key={opt.value}
              className={cn(
                'flex items-start gap-3 p-3 rounded-lg border transition-colors cursor-pointer',
                netpol === opt.value
                  ? 'border-foreground/40 bg-accent/40'
                  : 'border-border bg-background hover:bg-accent/30',
                !canEdit && 'opacity-60 cursor-not-allowed hover:bg-background',
              )}
            >
              <input
                type="radio"
                name="netpol"
                value={opt.value}
                checked={netpol === opt.value}
                disabled={!canEdit}
                onChange={() => setNetpol(opt.value)}
                className="mt-0.5"
              />
              <div>
                <p className="text-sm font-medium text-foreground">{opt.label}</p>
                <p className="text-xs text-muted-foreground mt-0.5">{opt.description}</p>
              </div>
            </label>
          ))}
        </div>
      </section>

      {canEdit && (
        <div className="flex justify-end">
          <button
            type="button"
            onClick={handleSave}
            disabled={updateMutation.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {updateMutation.isPending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Save className="h-3.5 w-3.5" />
            )}
            Save changes
          </button>
        </div>
      )}

      {/* --- Live quota usage table --- */}
      <section className="rounded-xl border border-border bg-card p-5 space-y-3">
        <header>
          <h2 className="text-sm font-medium text-foreground">Quota usage</h2>
          <p className="text-xs text-muted-foreground mt-0.5">
            Live ResourceQuota.status.used per cluster / namespace. Polls every 30 seconds.
          </p>
        </header>
        <div className="overflow-x-auto">
          <Table className="w-full text-sm">
            <TableHeader>
              <TableRow className="text-xs text-muted-foreground border-b border-border">
                <TableHead className="text-left font-medium py-2 px-3">Cluster</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Namespace</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">CPU</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Memory</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Pods</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {usage?.rows.length ? (
                usage.rows.map((row) => (
                  <TableRow
                    key={`${row.clusterId}/${row.namespace}`}
                    className="border-b border-border last:border-0"
                  >
                    <TableCell className="py-2 px-3 text-foreground">{row.clusterName}</TableCell>
                    <TableCell className="py-2 px-3 font-mono text-xs text-muted-foreground">
                      {row.namespace}
                    </TableCell>
                    <TableCell className="py-2 px-3 tabular-nums">
                      {row.cpuUsed || '0'} / {row.cpuLimit || '—'}
                    </TableCell>
                    <TableCell className="py-2 px-3 tabular-nums">
                      {row.memoryUsed || '0'} / {row.memoryLimit || '—'}
                    </TableCell>
                    <TableCell className="py-2 px-3 tabular-nums">
                      {row.podsUsed ?? 0} / {row.podsLimit ?? '—'}
                    </TableCell>
                  </TableRow>
                ))
              ) : (
                <TableRow>
                  <TableCell colSpan={5} className="py-6 text-center text-xs text-muted-foreground">
                    No quotas applied yet.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      </section>
    </div>
  );
}

function QuotaInput({
  label,
  placeholder,
  hint,
  value,
  disabled,
  onChange,
}: {
  label: string;
  placeholder: string;
  hint?: string;
  value: string;
  disabled?: boolean;
  onChange: (v: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">{label}</label>
      <input
        type="text"
        value={value}
        disabled={disabled}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring disabled:opacity-60 disabled:cursor-not-allowed"
      />
      <p className="text-xs text-muted-foreground">{hint || 'Empty = unlimited'}</p>
    </div>
  );
}

// ----- Quota string parsing helpers -----

/** Parse a Kubernetes CPU quantity (e.g. "500m", "2", "2.5") into cores. */
function parseCpu(input: string): number {
  if (!input) return 0;
  const trimmed = input.trim();
  if (trimmed.endsWith('m')) {
    const millis = Number(trimmed.slice(0, -1));
    return isFinite(millis) ? millis / 1000 : 0;
  }
  const n = Number(trimmed);
  return isFinite(n) ? n : 0;
}

/** Parse a Kubernetes memory quantity into MiB. Handles Mi/Gi/Ki/Ti binary suffixes. */
function parseMemMiB(input: string): number {
  if (!input) return 0;
  const m = /^([\d.]+)\s*([KMGT]i?|)\b/.exec(input.trim());
  if (!m) return 0;
  const n = Number(m[1]);
  if (!isFinite(n)) return 0;
  switch (m[2]) {
    case 'Ki':
      return n / 1024;
    case 'Mi':
      return n;
    case 'Gi':
      return n * 1024;
    case 'Ti':
      return n * 1024 * 1024;
    case 'K':
      return (n * 1000) / (1024 * 1024);
    case 'M':
      return (n * 1000 * 1000) / (1024 * 1024);
    case 'G':
      return (n * 1000 * 1000 * 1000) / (1024 * 1024);
    case 'T':
      return (n * 1000 * 1000 * 1000 * 1000) / (1024 * 1024);
    default:
      // bytes
      return n / (1024 * 1024);
  }
}

function formatMiB(mib: number): string {
  if (mib >= 1024) return `${(mib / 1024).toFixed(2)} GiB`;
  return `${mib.toFixed(0)} MiB`;
}

export const Route = createFileRoute('/dashboard/projects/$id/policy/')({
  component: PolicyPage,
});
