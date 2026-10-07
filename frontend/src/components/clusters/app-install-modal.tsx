import {
  CatalogVersionSelect,
  useCatalogVersionSelection,
} from "@/components/catalog/version-selection";
import { useOperationIntent } from "@/lib/use-operation-intent";
import { AppInstallValuesEditor } from "./app-install-values-editor";
import { useUpgradeValues } from "./app-upgrade-values";
import { useAppInstallPreview } from "./use-app-install-preview";
/**
 * App install / upgrade modal — sprint 082+.
 *
 * Shared component for both fresh installs (from Browse / Recommended)
 * and upgrades on already-installed releases (from the Installed row's
 * "Upgrade" action). Two key differences between the modes:
 *
 *   • mode='install' → POST /catalog/installed/, release_name + ns are
 *     editable, defaults to the chart name and its supported system namespace.
 *   • mode='upgrade' → PUT /catalog/installed/{id}/upgrade/, release_name
 *     + ns are read-only (those are the release identity), version
 *     dropdown is the user's actual control.
 *
 * Values editor:
 *   • Pre-filled from GET /catalog/charts/{chart_id}/values/?version=
 *     which lazy-hydrates the chart's defaults on first call (~1-2s)
 *     then caches.
 *   • On upgrade mode it's pre-filled with the release's current
 *     values_override so the user sees what they currently have, not
 *     a wall of fresh defaults to wade through.
 *   • Curated Settings and full YAML round-trip through the same override.
 *     Review is backed by the public server preview and blocks mutation when
 *     validation or preflight finds an unsafe configuration.
 *
 * Submission returns a durable catalog operation receipt. The caller tracks
 * that operation and its Flux rollout; acceptance is not workload readiness.
 */

import { QueryStates } from "@/components/ui/query-states";
import { useAppForm, useStore } from "@/lib/form";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import { ModalShell } from "@/components/ui/modal-shell";
import {
  installChartOnCluster,
  upgradeClusterApp,
} from "@/lib/api/cluster-apps";
import { recommendedCatalogNamespace } from "@/lib/catalog-chart-fields";
import { catalogInstallDefaultValues } from "@/lib/catalog-install-defaults";
import { parseHelmValuesYAML } from "@/lib/helm-values-schema";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { queryKeys } from "@/lib/query-keys";
import {
  AppInstallFooter,
  ChartInstallationNotes,
  HAS_CRDS,
} from "./app-install-parts";
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
      namespace:
        mode.kind === "upgrade"
          ? mode.namespace
          : recommendedCatalogNamespace(mode.chartName),
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
  const valuesYaml = useStore(form.store, (s) => s.values.valuesYaml);
  const [editorMode, setEditorMode] = useState<"form" | "yaml" | "review">(
    "form",
  );
  const [yamlError, setYamlError] = useState<string | null>(null);
  // Hydrate each selected version once so background renders preserve edits.
  const hydratedForVersion = useRef("");

  const versions = useCatalogVersionSelection(
    projectId,
    mode.chartId,
    selectedVersionId,
    (id) => form.setFieldValue("selectedVersionId", id),
  );
  const selectedVersion = versions.selected;

  const {
    defaultValues,
    valuesSchema,
    schemaValues,
    preview,
    blockingPreview,
  } = useAppInstallPreview({
    projectId,
    chartId: mode.chartId,
    chartName: mode.chartName,
    version: selectedVersion?.version,
    clusterId,
    selectedVersionId,
    namespace,
    valuesYaml,
    isUpgrade,
    editorMode,
  });

  useEffect(() => {
    if (isUpgrade) return; // don't auto-clobber on upgrade
    if (defaultValues.isError || !defaultValues.data) return;
    if (hydratedForVersion.current === selectedVersionId) return;
    form.setFieldValue(
      "valuesYaml",
      catalogInstallDefaultValues(
        mode.chartName,
        defaultValues.data.defaultValues,
      ),
    );
    hydratedForVersion.current = selectedVersionId;
  }, [
    defaultValues.data,
    defaultValues.isError,
    mode.chartName,
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
      if (
        value.valuesYaml.trim() &&
        parseHelmValuesYAML(value.valuesYaml) == null
      ) {
        throw new Error("Values must be valid YAML containing an object");
      }
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
    !yamlError &&
    !blockingPreview &&
    !(editorMode === "review" && (preview.isLoading || preview.isError)) &&
    !submitBlockedReason &&
    (!isUpgrade || (upgradeValues.isSuccess && !upgradeValues.isError));

  const handleSubmit = () => {
    if (submitBlockedReason) {
      toastWarning(submitBlockedReason);
      return;
    }
    if (valuesYaml.trim() && parseHelmValuesYAML(valuesYaml) == null) {
      setYamlError("Values must be valid YAML containing an object.");
      return;
    }
    setYamlError(null);
    void form.handleSubmit();
  };

  const switchEditorMode = (next: "form" | "yaml" | "review") => {
    if (next === "form" && schemaValues == null) {
      setYamlError("Fix the YAML before returning to the form.");
      return;
    }
    setYamlError(null);
    setEditorMode(next);
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
          reason={
            submitBlockedReason ??
            blockingPreview?.description ??
            yamlError ??
            undefined
          }
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

        <AppInstallValuesEditor
          defaultValues={defaultValues}
          isUpgrade={isUpgrade}
          valuesSchema={valuesSchema}
          editorMode={editorMode}
          switchEditorMode={switchEditorMode}
          preview={preview}
          chartName={mode.chartName}
          version={selectedVersion?.version ?? ""}
          namespace={namespace}
          releaseName={releaseName}
          schemaValues={schemaValues}
          valuesYaml={valuesYaml}
          onValuesChange={(value) => form.setFieldValue("valuesYaml", value)}
          setYamlError={setYamlError}
          yamlError={yamlError}
        />
      </div>
    </ModalShell>
  );
}
