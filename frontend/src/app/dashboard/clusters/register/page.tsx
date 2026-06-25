'use client';

// Wizard page 1 — form. Replaces the legacy <RegisterClusterModal />.
// On submit:
//   1. POST /clusters/   (creates the row at phase=created)
//   2. PUT  /clusters/{id}/registration/options/ (records install_baseline choice)
//   3. router.push(/dashboard/clusters/register/{id}/connect)
//
// The "Install Platform Baseline" checkbox defaults OFF — matches the
// Rancher posture called out in the sprint plan (operators must
// explicitly opt in to installing tools).

import { useRouter } from '@/lib/navigation';
import { useState } from 'react';
import { toastError } from '@/lib/toast';
import { Server, Loader2, Info } from 'lucide-react';
import { createCluster } from '@/lib/api';
import { setRegistrationOptions } from '@/lib/api';
import type { ClusterEnvironment } from '@/types';

export default function RegisterClusterWizardPage() {
  const router = useRouter();
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    name: '',
    displayName: '',
    description: '',
    environment: 'development' as ClusterEnvironment,
    region: '',
    installBaseline: false,
    privilegeProfile: 'viewer',
  });

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name) return;
    setSubmitting(true);
    try {
      const cluster = await createCluster({
        name: form.name,
        displayName: form.displayName || form.name,
        description: form.description || undefined,
        environment: form.environment,
        // distribution is auto-detected by the agent on connect (node labels /
        // providerID) and persisted via heartbeat — no manual choice needed.
        region: form.region || undefined,
        annotations: { 'astronomer.io/agent-privilege-profile': form.privilegeProfile },
      });
      // Record the operator's choice. The backend keeps install_baseline
      // NULL until this call so it can distinguish "hasn't decided" from
      // "opted out".
      await setRegistrationOptions(cluster.id, form.installBaseline);
      router.push(`/dashboard/clusters/register/${cluster.id}/connect`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Unknown error';
      toastError(`Failed to create cluster: ${msg}`);
      setSubmitting(false);
    }
  };

  return (
    <div className="max-w-3xl mx-auto p-6">
      <div className="mb-6">
        <div className="flex items-center gap-3 mb-2">
          <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
            <Server className="h-5 w-5 text-muted-foreground" />
          </div>
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Register cluster</h1>
            <p className="text-sm text-muted-foreground">Step 1 of 3 — Cluster details</p>
          </div>
        </div>
      </div>

      <form onSubmit={onSubmit} className="space-y-5">
        <Field label="Cluster name" required>
          <input
            type="text"
            value={form.name}
            onChange={(e) =>
              setForm((f) => ({ ...f, name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-') }))
            }
            placeholder="my-cluster"
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
            autoFocus
          />
        </Field>

        <Field label="Display name">
          <input
            type="text"
            value={form.displayName}
            onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
            placeholder="My Production Cluster"
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
          />
        </Field>

        <Field label="Description">
          <textarea
            value={form.description}
            onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
            placeholder="Brief description..."
            rows={2}
            className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm resize-none"
          />
        </Field>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="Environment">
            <select
              value={form.environment}
              onChange={(e) => setForm((f) => ({ ...f, environment: e.target.value as ClusterEnvironment }))}
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
            >
              <option value="development">Development</option>
              <option value="staging">Staging</option>
              <option value="production">Production</option>
              <option value="testing">Testing</option>
            </select>
          </Field>
          <Field label="Region">
            <input
              type="text"
              value={form.region}
              onChange={(e) => setForm((f) => ({ ...f, region: e.target.value }))}
              placeholder="us-east-1"
              className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
            />
          </Field>
        </div>

        <p className="text-xs text-muted-foreground -mt-2">
          Kubernetes distribution (k3s, RKE2, EKS, AKS, GKE, OpenShift…) is detected automatically from the cluster once the agent connects.
        </p>

        <Field label="Agent privilege profile">
          <select
            value={form.privilegeProfile}
            onChange={(e) => setForm((f) => ({ ...f, privilegeProfile: e.target.value }))}
            className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
          >
            <option value="viewer">Viewer — Astronomer observes (read-only)</option>
            <option value="admin">Admin — Astronomer operates (governed by user RBAC)</option>
          </select>
          <p className="mt-1 text-xs text-muted-foreground">
            Sets the ceiling for what Astronomer can do on this cluster.{' '}
            <span className="font-medium text-foreground">Viewer</span> is read-only — Astronomer can observe the cluster,
            and no user can change it regardless of their Astronomer role (safe first adoption, trivially removable).{' '}
            <span className="font-medium text-foreground">Admin</span> lets Astronomer operate the cluster; what each user
            can actually do is then governed by their Astronomer RBAC. (Finer-grained operator / namespace-scoped profiles
            are available via the API.)
          </p>
        </Field>

        <label className="flex items-start gap-3 p-4 rounded-lg border border-border bg-muted/20 cursor-pointer hover:bg-muted/30 transition-colors">
          <input
            type="checkbox"
            checked={form.installBaseline}
            onChange={(e) => setForm((f) => ({ ...f, installBaseline: e.target.checked }))}
            className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-ring"
          />
          <div className="flex-1">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium text-foreground">Quick Start: Install Platform Baseline after cluster connects</span>
              <Info className="h-3.5 w-3.5 text-muted-foreground" aria-label="Installs trivy-operator, kube-state-metrics, prometheus-node-exporter, fluent-bit, ingress-nginx, cert-manager, gatekeeper" />
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              Installs platform baseline components after the agent connects: trivy-operator,
              kube-state-metrics, prometheus-node-exporter, fluent-bit, ingress-nginx, cert-manager, and Gatekeeper.
              Leave unchecked for a bare cluster — you can install these later from the Cluster Tools tab.
            </p>
          </div>
        </label>

        <div className="flex items-center justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={() => router.push('/dashboard/clusters')}
            className="h-10 px-4 rounded-lg border border-border text-sm font-medium hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!form.name || submitting}
            className="inline-flex items-center gap-2 h-10 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {submitting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Next: Get install command →
          </button>
        </div>
      </form>
    </div>
  );
}

function Field({ label, required, children }: { label: string; required?: boolean; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-danger ml-1">*</span>}
      </label>
      {children}
    </div>
  );
}
