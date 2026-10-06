import { useOperationIntent } from "@/lib/use-operation-intent";
import { useUpgradeValues } from "./app-upgrade-values";
import {
  CatalogVersionSelect,
  useCatalogVersionSelection,
} from "@/components/catalog/version-selection";
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
 * Submission returns a durable catalog operation receipt. The caller tracks
 * that operation and its Flux rollout; acceptance is not workload readiness.
 */

import { useEffect, useRef } from "react";
import { QueryStates } from "@/components/ui/query-states";
import { useAppForm, useStore } from "@/lib/form";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";

import { ModalShell } from "@/components/ui/modal-shell";
import {
  getChartDefaultValues,
  installChartOnCluster,
  upgradeClusterApp,
} from "@/lib/api/cluster-apps";
import { queryKeys } from "@/lib/query-keys";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import {
  AppInstallFooter,
  ChartInstallationNotes,
  HAS_CRDS,
} from "./app-install-parts";
import { BareButton } from "@/components/form/bare-button";
export { AppUninstallModal } from "./app-uninstall-modal";

type Mode =
  | { kind: "install"; chartId: string; chartName: string }
  | {
      kind: "upgrade";
      installedChartId: string;
      chartId: string;
      chartName: string;
      currentVersionId: string;
      currentValues: string;
      releaseName: string;
      namespace: string;
    };

interface AppInstallModalProps {
  projectId: string;
  clusterId: string;
  mode: Mode;
  onClose: () => void;
  onOperationStarted?: (id: string) => void;
  submitDecision?: PermissionDecision;
}

// Charts whose first install commonly takes 5+ minutes due to CRDs /
// sub-chart dependencies. Shown as an info banner so operators don't
// panic when the row stays in 'installing' for a while.
const SLOW_INSTALL_CHARTS = new Set([
  "kube-prometheus-stack",
  "prometheus-operator",
  "cert-manager",
  "istio-base",
  "istiod",
  "kube-state-metrics",
  "loki-stack",
  "loki-distributed",
]);

export function AppInstallModal({
  projectId,
  clusterId,
  mode,
  onClose,
  submitDecision,
  onOperationStarted,
}: AppInstallModalProps) {
  const qc = useQueryClient();
  const isUpgrade = mode.kind === "upgrade";
  const submitBlockedReason =
    submitDecision && !submitDecision.allowed
      ? permissionDeniedReason(submitDecision)
      : undefined;

  const form = useAppForm({
    defaultValues: {
      selectedVersionId: mode.kind === "upgrade" ? mode.currentVersionId : "",
      releaseName: mode.kind === "upgrade" ? mode.releaseName : mode.chartName,
      namespace: mode.kind === "upgrade" ? mode.namespace : "default",
      valuesYaml: mode.kind === "upgrade" ? mode.currentValues : "",
    },
    onSubmit: () => install.mutate(),
  });
  const upgradeValues = useUpgradeValues(
    mode.kind === "upgrade" ? mode.installedChartId : "",
    (values) => form.setFieldValue("valuesYaml", values),
  );
  const selectedVersionId = useStore(
    form.store,
    (s) => s.values.selectedVersionId,
  );
  const releaseName = useStore(form.store, (s) => s.values.releaseName);
  const namespace = useStore(form.store, (s) => s.values.namespace);
  // Tracks whether we've already pre-filled defaults for the chosen
  // version — used so that switching versions in install mode
  // refreshes the YAML, but typing into the editor doesn't get
  // clobbered by a re-render of the same version.
  const hydratedForVersion = useRef("");

  const versions = useCatalogVersionSelection(
    projectId,
    mode.chartId,
    selectedVersionId,
    (id) => form.setFieldValue("selectedVersionId", id),
  );
  const selectedVersion = versions.selected;

  // Hydrate values.yaml when the version changes. In upgrade mode we
  // intentionally DON'T overwrite the user's current values with the
  // new version's defaults — that would silently revert their
  // customisation. Show a "Reset to chart defaults" button instead.
  const defaultValues = useQuery({
    queryKey: queryKeys.catalog.installChartValues(
      projectId,
      mode.chartId,
      selectedVersion?.version,
    ),
    queryFn: ({ signal }) =>
      getChartDefaultValues(
        projectId,
        mode.chartId,
        selectedVersion?.version,
        signal,
      ),
    enabled: !!projectId && !!selectedVersion?.version,
    throwOnError: false,
  });

  useEffect(() => {
    if (isUpgrade) return; // don't auto-clobber on upgrade
    if (defaultValues.isError || !defaultValues.data) return;
    const key = selectedVersionId;
    if (hydratedForVersion.current === key) return;
    form.setFieldValue("valuesYaml", defaultValues.data.defaultValues);
    hydratedForVersion.current = key;
  }, [
    defaultValues.data,
    defaultValues.isError,
    form,
    selectedVersionId,
    isUpgrade,
  ]);

  const intent = useOperationIntent();
  const install = useMutation({
    mutationFn: async () => {
      if (isUpgrade && (!upgradeValues.isSuccess || upgradeValues.isError))
        throw new Error("Load the saved release values before upgrading");
      const value = form.state.values;
      const idempotencyKey = intent.keyFor({
        mode,
        clusterId,
        projectId,
        ...value,
      });
      if (
        !selectedVersion ||
        selectedVersion.id !== value.selectedVersionId ||
        submitBlockedReason ||
        (!isUpgrade && (defaultValues.isError || defaultValues.isLoading))
      ) {
        throw new Error(
          "Select an available version and wait for its values before submitting.",
        );
      }
      if (mode.kind === "install") {
        return installChartOnCluster({
          projectId,
          clusterId,
          idempotencyKey,
          chartVersionId: value.selectedVersionId,
          releaseName: value.releaseName.trim(),
          namespace: value.namespace.trim(),
          valuesOverride: value.valuesYaml,
        });
      }
      // Upgrade — uses the existing /catalog/installed/{id}/upgrade/ endpoint.
      return upgradeClusterApp(
        mode.installedChartId,
        {
          chart_version_id: value.selectedVersionId,
          values_override: value.valuesYaml,
        },
        idempotencyKey,
      );
    },
    onSuccess: (receipt) => {
      intent.complete();
      if (receipt.operation?.id) onOperationStarted?.(receipt.operation.id);
      toastSuccess(
        isUpgrade
          ? `Upgrade dispatched — ${mode.kind === "upgrade" ? mode.releaseName : ""} will reflect new revision shortly`
          : `Install accepted — track "${releaseName}" and its Flux rollout in the operation timeline`,
      );
      qc.invalidateQueries({
        queryKey: queryKeys.clusterPages.appsInstalled(clusterId),
      });
      onClose();
    },
    onError: (err) => {
      toastApiError(`${isUpgrade ? "Upgrade" : "Install"} failed`, err);
    },
  });

  const submittable =
    !!selectedVersion &&
    (isUpgrade || (!defaultValues.isError && !defaultValues.isLoading)) &&
    !!selectedVersionId &&
    releaseName.trim() !== "" &&
    namespace.trim() !== "" &&
    !install.isPending &&
    !submitBlockedReason &&
    (!isUpgrade || (upgradeValues.isSuccess && !upgradeValues.isError));

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
      title={`${isUpgrade ? "Upgrade" : "Install"} ${mode.chartName}`}
      subtitle={
        isUpgrade
          ? "Change the version and/or values on an existing release."
          : "Configure version, namespace, release name, and values."
      }
      onClose={onClose}
      size="xl"
      panelClassName="max-w-3xl bg-popover flex flex-col overflow-hidden"
      bodyClassName="p-0"
      footerClassName="bg-muted/30 shrink-0"
      footer={
        <AppInstallFooter
          onClose={onClose}
          pending={install.isPending}
          onSubmit={handleSubmit}
          submittable={submittable}
          reason={submitBlockedReason}
          upgrade={isUpgrade}
        />
      }
    >
      <div className="flex-1 overflow-y-auto p-6 space-y-4">
        {isUpgrade && (
          <QueryStates
            query={upgradeValues}
            permission="catalog:read"
            errorTitle="Saved release values unavailable"
          >
            <></>
          </QueryStates>
        )}
        <ChartInstallationNotes
          slowInstall={slowInstall}
          hasCRDs={hasCRDs}
          isUpgrade={isUpgrade}
          chartName={mode.chartName}
        />

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div className="space-y-1.5">
            <label
              className="text-xs font-medium text-muted-foreground"
              htmlFor="field-ae92ffd8-268"
            >
              Version
            </label>
            <CatalogVersionSelect
              selection={versions}
              id="field-ae92ffd8-268"
            />
          </div>

          <div className="space-y-1.5">
            <label
              className="text-xs font-medium text-muted-foreground"
              htmlFor="field-ae92ffd8-296"
            >
              Release name
            </label>
            <form.Field name="releaseName">
              {(field) => (
                <input
                  id="field-ae92ffd8-296"
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  disabled={isUpgrade}
                  className="w-full h-(--control-h) px-3 rounded-md border border-border bg-background text-sm font-mono disabled:opacity-50 focus:outline-hidden focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label
              className="text-xs font-medium text-muted-foreground"
              htmlFor="field-ae92ffd8-312"
            >
              Namespace
            </label>
            <form.Field name="namespace">
              {(field) => (
                <input
                  id="field-ae92ffd8-312"
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  disabled={isUpgrade}
                  className="w-full h-(--control-h) px-3 rounded-md border border-border bg-background text-sm font-mono disabled:opacity-50 focus:outline-hidden focus:ring-1 focus:ring-ring"
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
                <span className="ml-2 text-2xs" aria-busy="true">
                  hydrating defaults…
                </span>
              )}
            </label>
            {defaultValues.isError && (
              <QueryStates
                query={defaultValues}
                permission="catalog:read"
                errorTitle="Could not load chart defaults"
              >
                {null}
              </QueryStates>
            )}
            {isUpgrade && !defaultValues.isError && defaultValues.data && (
              <BareButton
                tooltip="Replace with the upstream chart's default values for the selected version"
                onClick={() =>
                  form.setFieldValue(
                    "valuesYaml",
                    defaultValues.data!.defaultValues,
                  )
                }
                className="text-11 text-muted-foreground hover:text-foreground underline inline-block font-normal"
              >
                Reset to chart defaults
              </BareButton>
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
                className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono focus:outline-hidden focus:ring-1 focus:ring-ring resize-y"
                placeholder="# values.yaml — overrides applied on top of chart defaults"
              />
            )}
          </form.Field>
          <p className="text-11 text-muted-foreground">
            Vault references like{" "}
            <code className="font-mono">${`{vault://secret/path#key}`}</code>{" "}
            are resolved at install time. Sensitive values stay in Vault rather
            than this row.
          </p>
        </div>
      </div>
    </ModalShell>
  );
}
