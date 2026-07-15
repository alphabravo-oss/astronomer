'use client';

/**
 * App install / upgrade modal — sprint 082+.
 *
 * Shared component for both fresh installs (from Browse / Recommended)
 * and upgrades on already-installed releases (from the Installed row's
 * "Upgrade" action). Two key differences between the modes:
 *
 *   • mode='install' → POST /catalog/installed/, release_name + ns are
 *     editable, defaults to chart name / 'default'.
 *   • mode='upgrade' → PUT /catalog/installed/{id}/upgrade/, release_name
 *     + ns are read-only (those are the release identity), version
 *     dropdown is the user's actual control.
 *
 * YAML editor:
 *   • Pre-filled from GET /catalog/charts/{chart_id}/values/?version=
 *     which lazy-hydrates the chart's defaults on first call (~1-2s)
 *     then caches.
 *   • On upgrade mode it's pre-filled with the release's current
 *     values_override so the user sees what they currently have, not
 *     a wall of fresh defaults to wade through.
 *   • Plain <textarea> for v1 — Monaco / CodeMirror would be nice but
 *     out of scope. YAML correctness isn't validated client-side;
 *     helm install will fail clearly on bad YAML.
 *
 * The submit is async via the asynq queue (existing /catalog/installed/
 * handler enqueues a HelmInstall through the tunnel). The modal just
 * reports success when the row is created — the actual install state
 * surfaces through the Installed view's polling.
 */

import { useState, useEffect, useMemo } from 'react';
import { useAppForm, useStore } from '@/lib/form';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastSuccess, toastWarning } from '@/lib/toast';
import { Loader2, AlertTriangle, Info } from 'lucide-react';

import { ModalShell } from '@/components/ui/modal-shell';
import {
  listChartVersions,
  getChartDefaultValues,
  installChartOnCluster,
  type ChartVersionRow,
} from '@/lib/api/cluster-detail';
import { upgradeInstalledChart } from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';
import type { PermissionDecision } from '@/lib/permissions';

type Mode =
  | { kind: 'install'; chartId: string; chartName: string }
  | { kind: 'upgrade'; installedChartId: string; chartId: string; chartName: string; currentVersionId: string; currentValues: string; releaseName: string; namespace: string };

interface AppInstallModalProps {
  clusterId: string;
  mode: Mode;
  onClose: () => void;
  submitDecision?: PermissionDecision;
}

// Charts whose first install commonly takes 5+ minutes due to CRDs /
// sub-chart dependencies. Shown as an info banner so operators don't
// panic when the row stays in 'installing' for a while.
const SLOW_INSTALL_CHARTS = new Set([
  'kube-prometheus-stack',
  'prometheus-operator',
  'cert-manager',
  'istio-base',
  'istiod',
  'kube-state-metrics',
  'loki-stack',
  'loki-distributed',
]);

// Charts that ship CRDs by default — the operator should know that
// uninstall will not remove the CRDs unless they take extra steps.
// Surfaced on install too so the operator picks a stable namespace
// from the start.
const HAS_CRDS = new Set([
  'kube-prometheus-stack',
  'cert-manager',
  'trivy-operator',
  'istio-base',
  'gatekeeper',
  'opa-gatekeeper',
  'argocd',
  'argo-cd',
]);

function permissionDeniedReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

export function AppInstallModal({ clusterId, mode, onClose, submitDecision }: AppInstallModalProps) {
  const qc = useQueryClient();
  const isUpgrade = mode.kind === 'upgrade';
  const submitBlockedReason = submitDecision && !submitDecision.allowed
    ? permissionDeniedReason(submitDecision)
    : undefined;

  const form = useAppForm({
    defaultValues: {
      selectedVersionId: mode.kind === 'upgrade' ? mode.currentVersionId : '',
      releaseName: mode.kind === 'upgrade' ? mode.releaseName : mode.chartName,
      namespace: mode.kind === 'upgrade' ? mode.namespace : 'default',
      valuesYaml: mode.kind === 'upgrade' ? mode.currentValues : '',
    },
    onSubmit: () => install.mutate(),
  });
  const selectedVersionId = useStore(form.store, (s) => s.values.selectedVersionId);
  const releaseName = useStore(form.store, (s) => s.values.releaseName);
  const namespace = useStore(form.store, (s) => s.values.namespace);
  // Tracks whether we've already pre-filled defaults for the chosen
  // version — used so that switching versions in install mode
  // refreshes the YAML, but typing into the editor doesn't get
  // clobbered by a re-render of the same version.
  const [hydratedForVersion, setHydratedForVersion] = useState<string>('');

  // Versions
  const versions = useQuery({
    queryKey: queryKeys.catalog.installChartVersions(mode.chartId),
    queryFn: () => listChartVersions(mode.chartId),
  });

  // Default the version select to the first (latest by row order) once
  // versions land. In upgrade mode we keep the current version unless
  // the user picks a different one.
  useEffect(() => {
    if (!versions.data || versions.data.length === 0) return;
    if (selectedVersionId) return;
    form.setFieldValue('selectedVersionId', versions.data[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [versions.data, selectedVersionId]);

  const selectedVersion: ChartVersionRow | undefined = useMemo(
    () => versions.data?.find((v) => v.id === selectedVersionId),
    [versions.data, selectedVersionId],
  );

  // Hydrate values.yaml when the version changes. In upgrade mode we
  // intentionally DON'T overwrite the user's current values with the
  // new version's defaults — that would silently revert their
  // customisation. Show a "Reset to chart defaults" button instead.
  const defaultValues = useQuery({
    queryKey: queryKeys.catalog.installChartValues(mode.chartId, selectedVersion?.version),
    queryFn: () => getChartDefaultValues(mode.chartId, selectedVersion?.version),
    enabled: !!selectedVersion?.version,
  });

  useEffect(() => {
    if (isUpgrade) return; // don't auto-clobber on upgrade
    if (!defaultValues.data) return;
    const key = selectedVersionId;
    if (hydratedForVersion === key) return;
    form.setFieldValue('valuesYaml', defaultValues.data.defaultValues);
    setHydratedForVersion(key);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [defaultValues.data, hydratedForVersion, selectedVersionId, isUpgrade]);

  const install = useMutation({
    mutationFn: async () => {
      const value = form.state.values;
      if (mode.kind === 'install') {
        return installChartOnCluster({
          clusterId,
          chartVersionId: value.selectedVersionId,
          releaseName: value.releaseName.trim(),
          namespace: value.namespace.trim(),
          valuesOverride: value.valuesYaml,
        });
      }
      // Upgrade — uses the existing /catalog/installed/{id}/upgrade/ endpoint.
      return upgradeInstalledChart(mode.installedChartId, {
        chart_version_id: value.selectedVersionId,
        values_override: value.valuesYaml,
      });
    },
    onSuccess: () => {
      toastSuccess(
        isUpgrade
          ? `Upgrade dispatched — ${mode.kind === 'upgrade' ? mode.releaseName : ''} will reflect new revision shortly`
          : `Install dispatched — "${releaseName}" will appear in Installed once helm completes`,
      );
      qc.invalidateQueries({ queryKey: queryKeys.clusterPages.appsInstalled(clusterId) });
      onClose();
    },
    onError: (err) => {
      toastApiError(`${isUpgrade ? 'Upgrade' : 'Install'} failed`, err);
    },
  });

  const submittable =
    !!selectedVersionId &&
    releaseName.trim() !== '' &&
    namespace.trim() !== '' &&
    !install.isPending &&
    !submitBlockedReason;

  const handleSubmit = () => {
    if (submitBlockedReason) {
      toastWarning(submitBlockedReason);
      return;
    }
    void form.handleSubmit();
  };

  const slowInstall = SLOW_INSTALL_CHARTS.has(mode.chartName);
  const hasCRDs = HAS_CRDS.has(mode.chartName);

  return (
    <ModalShell
      title={`${isUpgrade ? 'Upgrade' : 'Install'} ${mode.chartName}`}
      subtitle={
        isUpgrade
          ? 'Change the version and/or values on an existing release.'
          : 'Configure version, namespace, release name, and values.'
      }
      onClose={onClose}
      size="xl"
      panelClassName="max-w-3xl bg-popover flex flex-col overflow-hidden"
      bodyClassName="p-0"
      footerClassName="bg-muted/30 flex-shrink-0"
      footer={
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="px-3 py-1.5 text-sm rounded-md border border-border bg-background hover:bg-muted"
            disabled={install.isPending}
          >
            Cancel
          </button>
          <button
            onClick={handleSubmit}
            disabled={!submittable}
            title={submitBlockedReason}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50"
          >
            {install.isPending ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> {isUpgrade ? 'Upgrading' : 'Installing'}…
              </>
            ) : (
              <>{isUpgrade ? 'Upgrade' : 'Install'}</>
            )}
          </button>
        </div>
      }
    >
        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          {(slowInstall || hasCRDs) && (
            <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs flex items-start gap-2">
              <Info className="h-4 w-4 text-amber-600 mt-0.5 flex-shrink-0" />
              <div className="space-y-0.5 text-foreground">
                {slowInstall && (
                  <div>
                    First install of <span className="font-medium">{mode.chartName}</span> typically takes 3–10 minutes — sub-charts and CRDs land before the workloads come up.
                  </div>
                )}
                {hasCRDs && !isUpgrade && (
                  <div>
                    This chart ships CRDs. The CRDs will <em>not</em> be removed automatically on uninstall (helm leaves them to protect data) — pick a stable namespace from the start.
                  </div>
                )}
              </div>
            </div>
          )}

          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground">Version</label>
              {versions.isLoading ? (
                <div className="h-9 flex items-center text-xs text-muted-foreground">
                  <Loader2 className="h-3.5 w-3.5 animate-spin mr-1.5" />
                  Loading versions…
                </div>
              ) : (
                <form.Field name="selectedVersionId">
                  {(field) => (
                    <select
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                      onBlur={field.handleBlur}
                      className="w-full h-9 px-2 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
                    >
                      {(versions.data ?? []).map((v) => (
                        <option key={v.id} value={v.id}>
                          {v.version}
                          {v.appVersion ? ` (app ${v.appVersion})` : ''}
                        </option>
                      ))}
                    </select>
                  )}
                </form.Field>
              )}
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground">Release name</label>
              <form.Field name="releaseName">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    disabled={isUpgrade}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono disabled:opacity-50 focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground">Namespace</label>
              <form.Field name="namespace">
                {(field) => (
                  <input
                    type="text"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    disabled={isUpgrade}
                    className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono disabled:opacity-50 focus:outline-none focus:ring-1 focus:ring-ring"
                  />
                )}
              </form.Field>
            </div>
          </div>

          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-muted-foreground">
                Values (YAML)
                {defaultValues.isLoading && (
                  <span className="ml-2 inline-flex items-center text-[10px] text-muted-foreground">
                    <Loader2 className="h-3 w-3 animate-spin mr-1" /> hydrating defaults…
                  </span>
                )}
              </label>
              {isUpgrade && defaultValues.data && (
                <button
                  onClick={() => form.setFieldValue('valuesYaml', defaultValues.data!.defaultValues)}
                  className="text-[11px] text-muted-foreground hover:text-foreground underline"
                  title="Replace with the upstream chart's default values for the selected version"
                >
                  Reset to chart defaults
                </button>
              )}
            </div>
            <form.Field name="valuesYaml">
              {(field) => (
                <textarea
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  rows={16}
                  spellCheck={false}
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono focus:outline-none focus:ring-1 focus:ring-ring resize-y"
                  placeholder="# values.yaml — overrides applied on top of chart defaults"
                />
              )}
            </form.Field>
            <p className="text-[11px] text-muted-foreground">
              Vault references like <code className="font-mono">${`{vault://secret/path#key}`}</code> are resolved at install time. Sensitive values stay in Vault rather than this row.
            </p>
          </div>
        </div>

    </ModalShell>
  );
}

// ---------------------------------------------------------------------
// Uninstall confirmation
// ---------------------------------------------------------------------

interface AppUninstallModalProps {
  clusterId: string;
  installedChartId: string;
  releaseName: string;
  chartName: string;
  namespace: string;
  onClose: () => void;
  onConfirm: () => Promise<void> | void;
  pending?: boolean;
  confirmDecision?: PermissionDecision;
}

export function AppUninstallModal({
  releaseName,
  chartName,
  namespace,
  onClose,
  onConfirm,
  pending,
  confirmDecision,
}: AppUninstallModalProps) {
  const [typed, setTyped] = useState('');
  const confirmBlockedReason = confirmDecision && !confirmDecision.allowed
    ? permissionDeniedReason(confirmDecision)
    : undefined;
  const confirmable = typed === releaseName && !pending && !confirmBlockedReason;
  const crdsWillSurvive = HAS_CRDS.has(chartName);
  const handleConfirm = () => {
    if (confirmBlockedReason) {
      toastWarning(confirmBlockedReason);
      return;
    }
    onConfirm();
  };

  return (
    <ModalShell
      title="Uninstall release"
      onClose={onClose}
      size="sm"
      panelClassName="bg-popover"
      bodyClassName="p-5 space-y-3 text-sm"
      footerClassName="bg-muted/30"
      titleIcon={<AlertTriangle className="h-5 w-5 text-red-500" />}
      footer={
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            className="px-3 py-1.5 text-sm rounded-md border border-border bg-background hover:bg-muted"
            disabled={pending}
          >
            Cancel
          </button>
          <button
            onClick={handleConfirm}
            disabled={!confirmable}
            title={confirmBlockedReason}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md bg-red-600 text-white hover:bg-red-700 disabled:opacity-50"
          >
            {pending ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Uninstalling…
              </>
            ) : (
              <>Uninstall</>
            )}
          </button>
        </div>
      }
    >
      <p>
        This will run <code className="font-mono text-xs">helm uninstall {releaseName} -n {namespace}</code> on the cluster.
        Workload pods + Services + ConfigMaps owned by the release will be deleted.
      </p>
      {crdsWillSurvive && (
        <div className="rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-xs">
          <div className="font-medium text-amber-600 flex items-center gap-1.5">
            <AlertTriangle className="h-3.5 w-3.5" /> CRDs will not be removed
          </div>
          <p className="text-muted-foreground mt-1">
            <span className="font-mono">{chartName}</span> ships CRDs. Helm leaves them in place on uninstall to protect data; remove manually with <code className="font-mono">kubectl delete crd …</code> if you need a clean re-install.
          </p>
        </div>
      )}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-muted-foreground">
          Type <code className="font-mono text-xs bg-muted px-1 rounded">{releaseName}</code> to confirm
        </label>
        <input
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring"
          autoFocus
        />
      </div>
    </ModalShell>
  );
}
