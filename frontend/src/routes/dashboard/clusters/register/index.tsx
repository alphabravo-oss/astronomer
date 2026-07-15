import { createFileRoute } from '@tanstack/react-router';
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
import { toastError } from '@/lib/toast';
import { Server, Loader2, Info, AlertTriangle } from 'lucide-react';
import { createCluster } from '@/lib/api';
import { setRegistrationOptions } from '@/lib/api';
import { useClusters } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';
import type { ClusterEnvironment } from '@/types';

function RegisterClusterWizardPage() {
  const router = useRouter();

  // Live name-availability check: cluster names are unique, so warn before
  // submit rather than letting the create POST come back 409.
  const { data: clustersData } = useClusters({ pageSize: 1000 });
  const existingNames = new Set((clustersData?.data ?? []).map((c) => c.name.toLowerCase()));

  const form = useAppForm({
    defaultValues: {
      name: '',
      displayName: '',
      description: '',
      environment: 'development' as ClusterEnvironment,
      region: '',
      installBaseline: false,
      privilegeProfile: 'viewer',
    },
    onSubmit: async ({ value }) => {
      // Old guard (`if (!form.name || nameTaken) return`) — the submit button's
      // disabled gate below is the same condition; re-checked here 1:1.
      if (!value.name || existingNames.has(value.name)) return;
      try {
        const cluster = await createCluster({
          name: value.name,
          displayName: value.displayName || value.name,
          description: value.description || undefined,
          environment: value.environment,
          // distribution is auto-detected by the agent on connect (node labels /
          // providerID) and persisted via heartbeat — no manual choice needed.
          region: value.region || undefined,
          annotations: { 'astronomer.io/agent-privilege-profile': value.privilegeProfile },
        });
        // Record the operator's choice. The backend keeps install_baseline
        // NULL until this call so it can distinguish "hasn't decided" from
        // "opted out".
        await setRegistrationOptions(cluster.id, value.installBaseline);
        router.push(`/dashboard/clusters/register/${cluster.id}/connect`);
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Unknown error';
        toastError(`Failed to register cluster: ${msg}`);
      }
    },
  });

  const name = useStore(form.store, (s) => s.values.name);
  const submitting = useStore(form.store, (s) => s.isSubmitting);
  const nameTaken = name.length > 0 && existingNames.has(name);

  return (
    <div className="max-w-3xl mx-auto p-6">
      <div className="mb-6">
        <div className="flex items-center gap-3 mb-2">
          <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
            <Server className="h-5 w-5 text-muted-foreground" />
          </div>
          <div>
            <h1 className="text-2xl font-semibold text-foreground">Register an existing cluster</h1>
            <p className="text-sm text-muted-foreground">Step 1 of 3 — Cluster details</p>
          </div>
        </div>
        <p className="text-sm text-muted-foreground">
          Connect a Kubernetes cluster you already run so Astronomer can observe and manage it.
          Astronomer does not create clusters, provision infrastructure, or add nodes — you install
          a lightweight agent and it adopts the cluster as-is.
        </p>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          void form.handleSubmit();
        }}
        className="space-y-5"
      >
        <Field label="Cluster name" required>
          <form.Field name="name">
            {(field) => (
              <input
                type="text"
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))
                }
                onBlur={field.handleBlur}
                placeholder="my-cluster"
                className={`w-full h-10 px-3 rounded-lg border bg-background text-sm ${
                  nameTaken ? 'border-status-danger' : 'border-border'
                }`}
                autoFocus
              />
            )}
          </form.Field>
          {nameTaken && (
            <p className="mt-1 flex items-center gap-1.5 text-xs text-status-danger">
              <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
              A cluster named &quot;{name}&quot; already exists. Choose a different name.
            </p>
          )}
        </Field>

        <Field label="Display name">
          <form.Field name="displayName">
            {(field) => (
              <input
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="My Production Cluster"
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
              />
            )}
          </form.Field>
        </Field>

        <Field label="Description">
          <form.Field name="description">
            {(field) => (
              <textarea
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Brief description..."
                rows={2}
                className="w-full px-3 py-2 rounded-lg border border-border bg-background text-sm resize-none"
              />
            )}
          </form.Field>
        </Field>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="Environment">
            <form.Field name="environment">
              {(field) => (
                <select
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value as ClusterEnvironment)}
                  onBlur={field.handleBlur}
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
                >
                  <option value="development">Development</option>
                  <option value="staging">Staging</option>
                  <option value="production">Production</option>
                  <option value="testing">Testing</option>
                </select>
              )}
            </form.Field>
          </Field>
          <Field label="Region">
            <form.Field name="region">
              {(field) => (
                <input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="us-east-1"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
                />
              )}
            </form.Field>
          </Field>
        </div>

        <p className="text-xs text-muted-foreground -mt-2">
          Kubernetes distribution (k3s, RKE2, EKS, AKS, GKE, OpenShift…) is detected automatically from the cluster once the agent connects.
        </p>

        <Field label="Agent privilege profile">
          <form.Field name="privilegeProfile">
            {(field) => (
              <select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm"
              >
                <option value="viewer">Viewer — Astronomer observes (read-only)</option>
                <option value="admin">Admin — Astronomer operates (governed by user RBAC)</option>
              </select>
            )}
          </form.Field>
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
          <form.Field name="installBaseline">
            {(field) => (
              <input
                type="checkbox"
                checked={field.state.value}
                onChange={(e) => field.handleChange(e.target.checked)}
                onBlur={field.handleBlur}
                className="mt-0.5 h-4 w-4 rounded border-border text-primary focus:ring-ring"
              />
            )}
          </form.Field>
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
            disabled={!name || nameTaken || submitting}
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

export const Route = createFileRoute('/dashboard/clusters/register/')({
  component: RegisterClusterWizardPage,
});
