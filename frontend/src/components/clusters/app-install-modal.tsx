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

import { useState, useEffect, useMemo, useRef } from "react";
import { HelmValuesForm } from "@/components/catalog/helm-values-form";
import { QueryStates } from "@/components/ui/query-states";
import { useAppForm, useStore } from "@/lib/form";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";
import {
  Loader2,
  AlertTriangle,
  Braces,
  FileCode2,
  GitCompare,
  Info,
} from "lucide-react";

import { ModalShell } from "@/components/ui/modal-shell";
import {
  getChartDefaultValues,
  installChartOnCluster,
  previewCatalogApplication,
  upgradeClusterApp,
} from "@/lib/api/cluster-apps";
import { queryKeys } from "@/lib/query-keys";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import {
  dumpHelmValuesYAML,
  hasRenderableSchema,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  resolveSchemaRefs,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";
import { cn } from "@/lib/utils";
import {
  curateHelmValuesSchema,
  recommendedCatalogNamespace,
} from "@/lib/catalog-chart-fields";
import { catalogInstallDefaultValues } from "@/lib/catalog-install-defaults";

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

// Charts that ship CRDs by default — the operator should know that
// uninstall will not remove the CRDs unless they take extra steps.
// Surfaced on install too so the operator picks a stable namespace
// from the start.
const HAS_CRDS = new Set([
  "kube-prometheus-stack",
  "cert-manager",
  "trivy-operator",
  "istio-base",
  "gatekeeper",
  "opa-gatekeeper",
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
  const valuesSchema = useMemo(() => {
    const resolved = resolveSchemaRefs(defaultValues.data?.valuesSchema);
    const renderable = hasRenderableSchema(resolved)
      ? (resolved as HelmValuesSchemaNode)
      : null;
    return curateHelmValuesSchema(mode.chartName, renderable);
  }, [defaultValues.data?.valuesSchema, mode.chartName]);
  const schemaValues = useMemo(() => {
    const parsed = parseHelmValuesYAML(valuesYaml);
    if (parsed == null) return null;
    return (
      valuesSchema ? mergeSchemaDefaults(valuesSchema, parsed) : parsed
    ) as HelmValuesObject;
  }, [valuesSchema, valuesYaml]);
  const valuesAreValid = !valuesYaml.trim() || schemaValues != null;
  const preview = useQuery({
    queryKey: queryKeys.catalog.applicationPreview(
      clusterId,
      selectedVersionId,
      namespace,
      valuesYaml,
      isUpgrade ? "upgrade" : "install",
    ),
    queryFn: () =>
      previewCatalogApplication({
        clusterId,
        chartVersionId: selectedVersionId,
        namespace: namespace.trim(),
        valuesOverride: valuesYaml,
        operation: isUpgrade ? "upgrade" : "install",
      }),
    enabled:
      editorMode === "review" &&
      !!selectedVersionId &&
      !!namespace.trim() &&
      valuesAreValid,
    retry: false,
  });
  const blockingPreview = preview.data?.checks.find(
    (check) => check.status === "blocking",
  );

  useEffect(() => {
    if (isUpgrade) return; // don't auto-clobber on upgrade
    if (defaultValues.isError || !defaultValues.data) return;
    const key = selectedVersionId;
    if (hydratedForVersion.current === key) return;
    form.setFieldValue(
      "valuesYaml",
      catalogInstallDefaultValues(
        mode.chartName,
        defaultValues.data.defaultValues,
      ),
    );
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
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono disabled:opacity-50 focus:outline-hidden focus:ring-1 focus:ring-ring"
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
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono disabled:opacity-50 focus:outline-hidden focus:ring-1 focus:ring-ring"
                />
              )}
            </form.Field>
          </div>
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <label className="text-xs font-medium text-muted-foreground">
              Values
              {defaultValues.isLoading && (
                <span className="ml-2 inline-flex items-center text-[10px] text-muted-foreground">
                  <Loader2 className="h-3 w-3 animate-spin mr-1" /> hydrating
                  defaults…
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
              <button
                onClick={() =>
                  form.setFieldValue(
                    "valuesYaml",
                    defaultValues.data!.defaultValues,
                  )
                }
                className="text-[11px] text-muted-foreground hover:text-foreground underline"
                title="Replace with the upstream chart's default values for the selected version"
              >
                Reset to chart defaults
              </button>
            )}
          </div>
          {valuesSchema && (
            <div className="inline-flex rounded-md border border-border bg-muted/30 p-1">
              <button
                type="button"
                aria-pressed={editorMode === "form"}
                onClick={() => switchEditorMode("form")}
                className={cn(
                  "inline-flex items-center gap-1 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors",
                  editorMode === "form"
                    ? "bg-background text-foreground shadow-xs"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                <Braces className="h-3.5 w-3.5" /> Form
              </button>
              <button
                type="button"
                aria-pressed={editorMode === "yaml"}
                onClick={() => switchEditorMode("yaml")}
                className={cn(
                  "inline-flex items-center gap-1 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors",
                  editorMode === "yaml"
                    ? "bg-background text-foreground shadow-xs"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                <FileCode2 className="h-3.5 w-3.5" /> YAML
              </button>
            </div>
          )}
          <button
            type="button"
            aria-pressed={editorMode === "review"}
            onClick={() => switchEditorMode("review")}
            className={cn(
              "inline-flex items-center gap-1 rounded-sm border border-border px-2.5 py-1 text-xs font-medium transition-colors",
              editorMode === "review"
                ? "bg-background text-foreground shadow-xs"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            <GitCompare className="h-3.5 w-3.5" /> Review
          </button>
          {editorMode === "review" ? (
            <CatalogApplicationReview
              preview={preview.data}
              loading={preview.isLoading}
              error={preview.error}
              chartName={mode.chartName}
              version={selectedVersion?.version ?? ""}
              namespace={namespace}
              releaseName={releaseName}
            />
          ) : valuesSchema && editorMode === "form" && schemaValues ? (
            <div className="rounded-lg border border-border bg-muted/20 p-4">
              <HelmValuesForm
                schema={valuesSchema}
                value={schemaValues}
                onChange={(next) => {
                  form.setFieldValue("valuesYaml", dumpHelmValuesYAML(next));
                  setYamlError(null);
                }}
              />
            </div>
          ) : (
            <form.Field name="valuesYaml">
              {(field) => (
                <textarea
                  aria-label="Values (YAML)"
                  value={field.state.value}
                  onChange={(e) => {
                    field.handleChange(e.target.value);
                    setYamlError(null);
                  }}
                  onBlur={() => {
                    field.handleBlur();
                    if (
                      field.state.value.trim() &&
                      parseHelmValuesYAML(field.state.value) == null
                    )
                      setYamlError(
                        "Values must be valid YAML containing an object.",
                      );
                  }}
                  rows={16}
                  spellCheck={false}
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-xs font-mono focus:outline-hidden focus:ring-1 focus:ring-ring resize-y"
                  placeholder="# values.yaml — overrides applied on top of chart defaults"
                />
              )}
            </form.Field>
          )}
          {yamlError && (
            <p role="alert" className="text-xs text-status-error">
              {yamlError}
            </p>
          )}
          <p className="text-[11px] text-muted-foreground">
            Use chart fields that reference an existing Kubernetes Secret for
            credentials. Verified Apps reject inline Vault placeholders so the
            durable delivery bundle never persists a hidden secret template.
          </p>
        </div>
      </div>
    </ModalShell>
  );
}

function CatalogApplicationReview({
  preview,
  loading,
  error,
  chartName,
  version,
  namespace,
  releaseName,
}: {
  preview?: OpenAPIComponents["schemas"]["CatalogInstallationPreview"];
  loading: boolean;
  error: Error | null;
  chartName: string;
  version: string;
  namespace: string;
  releaseName: string;
}) {
  if (loading)
    return (
      <p className="text-sm text-muted-foreground">
        Validating trust, compatibility, access and configuration…
      </p>
    );
  if (error)
    return (
      <p role="alert" className="text-sm text-status-error">
        Preview failed: {error.message}
      </p>
    );
  if (!preview) return null;
  return (
    <div className="space-y-4">
      <div className="rounded-md border border-border bg-muted/20 px-3 py-2 text-xs">
        <p className="font-medium text-foreground">
          {releaseName} · {chartName}@{version}
        </p>
        <p className="text-muted-foreground">Namespace {namespace}</p>
      </div>
      <section aria-label="Installation checks" className="space-y-2">
        {preview.checks.map((check) => (
          <div
            key={check.code}
            className={cn(
              "rounded-md border px-3 py-2 text-xs",
              check.status === "blocking"
                ? "border-status-error/30 bg-status-error/5"
                : check.status === "advisory" || check.status === "approval"
                  ? "border-status-warning/30 bg-status-warning/5"
                  : "border-status-success/30 bg-status-success/5",
            )}
          >
            <p className="font-semibold text-foreground">{check.title}</p>
            <p className="mt-0.5 text-muted-foreground">{check.description}</p>
          </div>
        ))}
      </section>
      <dl className="grid gap-2 text-[11px] text-muted-foreground sm:grid-cols-2">
        <div>
          <dt>Artifact digest</dt>
          <dd className="truncate font-mono" title={preview.artifact_digest}>
            {preview.artifact_digest}
          </dd>
        </div>
        <div>
          <dt>Values digest</dt>
          <dd className="truncate font-mono" title={preview.values_digest}>
            {preview.values_digest}
          </dd>
        </div>
      </dl>
    </div>
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
  const [typed, setTyped] = useState("");
  const confirmBlockedReason =
    confirmDecision && !confirmDecision.allowed
      ? permissionDeniedReason(confirmDecision)
      : undefined;
  const confirmable =
    typed === releaseName && !pending && !confirmBlockedReason;
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
      titleIcon={<AlertTriangle className="h-5 w-5 text-status-error" />}
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
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md bg-status-error text-background hover:bg-status-error disabled:opacity-50"
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
        This will run{" "}
        <code className="font-mono text-xs">
          helm uninstall {releaseName} -n {namespace}
        </code>{" "}
        on the cluster. Workload pods + Services + ConfigMaps owned by the
        release will be deleted.
      </p>
      {crdsWillSurvive && (
        <div className="rounded-md border border-status-warning/40 bg-status-warning/5 px-3 py-2 text-xs">
          <div className="font-medium text-status-warning flex items-center gap-1.5">
            <AlertTriangle className="h-3.5 w-3.5" /> CRDs will not be removed
          </div>
          <p className="text-muted-foreground mt-1">
            <span className="font-mono">{chartName}</span> ships CRDs. Helm
            leaves them in place on uninstall to protect data; remove manually
            with <code className="font-mono">kubectl delete crd …</code> if you
            need a clean re-install.
          </p>
        </div>
      )}
      <div className="space-y-1.5">
        <label className="text-xs font-medium text-muted-foreground">
          Type{" "}
          <code className="font-mono text-xs bg-muted px-1 rounded-sm">
            {releaseName}
          </code>{" "}
          to confirm
        </label>
        <input
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono focus:outline-hidden focus:ring-1 focus:ring-ring"
          data-initial-focus
        />
      </div>
    </ModalShell>
  );
}

function AppInstallFooter({
  onClose,
  pending,
  onSubmit,
  submittable,
  reason,
  upgrade,
}: {
  onClose: () => void;
  pending: boolean;
  onSubmit: () => void;
  submittable: boolean;
  reason?: string;
  upgrade: boolean;
}) {
  return (
    <div className="flex items-center justify-end gap-2">
      <button
        onClick={onClose}
        className="px-3 py-1.5 text-sm rounded-md border border-border bg-background hover:bg-muted"
        disabled={pending}
      >
        Cancel
      </button>
      <button
        onClick={onSubmit}
        disabled={!submittable}
        title={reason}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-md bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50"
      >
        {pending ? (
          <>
            <Loader2 className="h-3.5 w-3.5 animate-spin" />{" "}
            {upgrade ? "Upgrading" : "Installing"}…
          </>
        ) : (
          <>{upgrade ? "Upgrade" : "Install"}</>
        )}
      </button>
    </div>
  );
}

function ChartInstallationNotes({
  slowInstall,
  hasCRDs,
  isUpgrade,
  chartName,
}: {
  slowInstall: boolean;
  hasCRDs: boolean;
  isUpgrade: boolean;
  chartName: string;
}) {
  return (
    <>
      {" "}
      {(slowInstall || hasCRDs) && (
        <div className="rounded-md border border-status-warning/30 bg-status-warning/5 px-3 py-2 text-xs flex items-start gap-2">
          <Info className="h-4 w-4 text-status-warning mt-0.5 shrink-0" />
          <div className="space-y-0.5 text-foreground">
            {slowInstall && (
              <div>
                First install of{" "}
                <span className="font-medium">{chartName}</span> typically takes
                3–10 minutes — sub-charts and CRDs land before the workloads
                come up.
              </div>
            )}
            {hasCRDs && !isUpgrade && (
              <div>
                This chart ships CRDs. The CRDs will <em>not</em> be removed
                automatically on uninstall (helm leaves them to protect data) —
                pick a stable namespace from the start.
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
