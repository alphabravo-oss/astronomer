import { ToolCatalogFields } from "./tool-catalog-fields";
import { CatalogOfferingSupport } from "@/components/catalog/catalog-offering-support";
import { OfferingSupport } from "@/components/catalog/offering-support";
import { InstallationDestination } from "@/components/catalog/installation-destination";
import { ActionButton } from "@/components/ui/action-button";
import { ConfigurationPage } from "@/components/catalog/configuration-page";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { toastWarning } from "@/lib/toast";
import type { ClusterTool, ToolFormField } from "@/types";
import * as yaml from "js-yaml";
import { useMemo, useState } from "react";
import {
  EditorMode,
  PresetSelector,
  ToolInstallFooter,
  ToolYamlEditor,
} from "./tool-install-controls";
import {
  coerce,
  groupFields,
  parseOverride,
  withPath,
  withoutPath,
} from "./tool-install-values";
import { ToolSettingsEditor } from "./tool-settings-editor";
import { previewToolFieldValues } from "./tool-values";
import { ToolValuesReview } from "./tool-values-review";
import { useToolInstallPreview } from "./use-tool-install-preview";

interface ToolInstallPageProps {
  tool: ClusterTool;
  clusterId: string;
  preset: string;
  onConfirm: (valuesOverride: string | undefined, preset: string) => void;
  onClose: () => void;
  installing?: boolean;
  confirmDecision?: PermissionDecision;
  action?: "install" | "upgrade";
  initialValuesYaml?: string;
  initialPreset?: string;
  loadingInitialValues?: boolean;
  initialValuesError?: Error | null;
}

const EMPTY_TOOL_FIELDS: ToolFormField[] = [];

export function ToolInstallPage(props: ToolInstallPageProps) {
  if (props.action === "upgrade" && props.initialValuesYaml === undefined) {
    return (
      <ConfigurationPage
        title={`Configure ${props.tool.name}`}
        onClose={props.onClose}
        footer={<ActionButton onClick={props.onClose}>Close</ActionButton>}
      >
        <p
          role={props.initialValuesError ? "alert" : "status"}
          className={
            props.initialValuesError
              ? "text-sm text-status-error"
              : "text-sm text-muted-foreground"
          }
        >
          {props.initialValuesError
            ? `Saved configuration unavailable: ${props.initialValuesError.message}`
            : "Loading the saved configuration…"}
        </p>
      </ConfigurationPage>
    );
  }
  return <ToolInstallPageEditor {...props} />;
}

function ToolInstallPageEditor({
  tool,
  clusterId,
  preset,
  onConfirm,
  onClose,
  installing,
  confirmDecision,
  action = "install",
  initialValuesYaml,
  initialPreset,
  loadingInitialValues,
  initialValuesError,
}: ToolInstallPageProps) {
  const isUpgrade = action === "upgrade";
  const fields = tool.formSchema?.fields ?? EMPTY_TOOL_FIELDS;
  const hasForm = fields.length > 0;
  const [mode, setMode] = useState<EditorMode>(hasForm ? "form" : "yaml");
  // Presets belong to the install editor; changing one refreshes its preview.
  const presetNames = useMemo(() => Object.keys(tool.presets), [tool.presets]);
  const [selectedPreset, setSelectedPreset] = useInitialToolPreset(
    presetNames,
    initialPreset ?? preset,
  );

  // Only operator edits override the selected preset. Schema display defaults
  // must never silently replace development/production sizing.
  const [overrideValues, setOverrideValues] = useState<Record<string, unknown>>(
    () => parseOverride(initialValuesYaml ?? "") ?? {},
  );
  const [yamlText, setYamlText] = useState(initialValuesYaml ?? "");
  const [yamlError, setYamlError] = useState<string | null>(() =>
    initialValuesYaml !== undefined && parseOverride(initialValuesYaml) == null
      ? "The saved tool configuration is not valid YAML."
      : null,
  );
  const [editedPaths, setEditedPaths] = useState<Set<string>>(() => new Set());

  const { data: preview, isLoading } = useToolInstallPreview(
    tool.slug,
    clusterId,
    selectedPreset,
    isUpgrade ? (initialValuesYaml ?? "") : "",
    !isUpgrade || initialValuesYaml !== undefined,
  );
  const charts = preview?.charts ?? [];
  const presetValues = useMemo(
    () => previewToolFieldValues(preview?.charts ?? [], tool, fields),
    [preview?.charts, tool, fields],
  );

  const groups = useMemo(() => groupFields(fields), [fields]);

  const overrideYaml = useMemo(
    () =>
      Object.keys(overrideValues).length
        ? yaml.dump(overrideValues, { lineWidth: -1, noRefs: true })
        : "",
    [overrideValues],
  );

  const effectivePreview = useToolInstallPreview(
    tool.slug,
    clusterId,
    selectedPreset,
    overrideYaml,
    mode === "review" &&
      !yamlError &&
      (!isUpgrade || initialValuesYaml !== undefined),
  );

  const preflightChecks =
    effectivePreview.data?.checks ?? preview?.checks ?? [];
  const blockingCheck = preflightChecks.find(
    (check) => check.status === "block",
  );

  const switchToYaml = () => {
    setYamlText(overrideYaml);
    setYamlError(null);
    setMode("yaml");
  };

  const applyYaml = (): boolean => {
    const parsed = parseOverride(yamlText);
    if (parsed == null) {
      setYamlError("Values override must be valid YAML containing an object.");
      return false;
    }
    setOverrideValues(parsed);
    setYamlError(null);
    return true;
  };

  const switchMode = (next: EditorMode) => {
    if (mode === "yaml" && !applyYaml()) return;
    if (next === "yaml") {
      switchToYaml();
      return;
    }
    setMode(next);
  };

  const confirmBlockedReason =
    confirmDecision && !confirmDecision.allowed
      ? permissionDeniedReason(confirmDecision)
      : undefined;

  const handleConfirm = () => {
    if (confirmBlockedReason) {
      toastWarning(confirmBlockedReason);
      return;
    }
    let override: string | undefined;
    if (mode === "yaml") {
      if (!applyYaml()) return;
      override = yamlText.trim() || undefined;
    } else {
      override = overrideYaml || undefined;
    }
    onConfirm(override, selectedPreset);
  };

  return (
    <ConfigurationPage
      title={`${isUpgrade ? "Configure" : "Install"} ${tool.name}`}
      subtitle={
        isUpgrade
          ? "Review saved values, change common settings or edit the complete YAML, then preview the exact release plan."
          : "Choose a preset, adjust common settings, and review the exact release plan before installation."
      }
      onClose={onClose}
      headerActions={
        charts.length ? (
          <ol
            aria-label="Release installation order"
            className="text-xs text-muted-foreground font-mono space-y-1"
          >
            {charts.map((chart, index) => (
              <li key={`${chart.namespace}/${chart.chartName}`}>
                {index + 1}. {chart.releaseName ?? chart.chartName} ·{" "}
                {chart.chartName}@{chart.chartVersion} · {chart.namespace}
              </li>
            ))}
          </ol>
        ) : undefined
      }
      footer={
        <ToolInstallFooter
          hasForm={hasForm}
          mode={mode}
          onModeChange={switchMode}
          onClose={onClose}
          onConfirm={handleConfirm}
          installing={installing}
          action={action}
          disabled={
            isLoading ||
            !!loadingInitialValues ||
            !!initialValuesError ||
            !!confirmBlockedReason ||
            !!yamlError ||
            !!blockingCheck
          }
          disabledReason={
            confirmBlockedReason ??
            initialValuesError?.message ??
            blockingCheck?.message
          }
        />
      }
    >
      <ToolInstallationSummary
        clusterId={clusterId}
        tool={tool}
        charts={charts}
      />
      <PresetSelector
        names={presetNames}
        value={selectedPreset}
        onChange={setSelectedPreset}
        isUpgrade={isUpgrade}
      />

      {mode === "form" && (
        <ToolCatalogFields
          charts={charts}
          clusterId={clusterId}
          overrides={overrideValues}
          onChange={setOverrideValues}
        />
      )}
      {mode === "form" ? (
        <ToolSettingsEditor
          groups={groups}
          overrides={overrideValues}
          presetValues={presetValues}
          editedPaths={editedPaths}
          isUpgrade={isUpgrade}
          onChange={(field, value) => {
            setEditedPaths((previous) => new Set(previous).add(field.path));
            setOverrideValues((previous) =>
              withPath(previous, field.path, coerce(field, value)),
            );
          }}
          onReset={(field) => {
            setEditedPaths((previous) => new Set(previous).add(field.path));
            setOverrideValues((previous) => withoutPath(previous, field.path));
          }}
          onStorageClassChange={(path, value) =>
            setOverrideValues((previous) => withPath(previous, path, value))
          }
        />
      ) : mode === "yaml" ? (
        <ToolYamlEditor
          value={yamlText}
          error={yamlError}
          onChange={(value) => {
            setYamlText(value);
            setYamlError(null);
          }}
          onBlur={applyYaml}
        />
      ) : (
        <ToolValuesReview
          baseline={charts}
          effective={effectivePreview.data?.charts ?? []}
          loading={effectivePreview.isLoading}
          error={effectivePreview.error}
          checks={preflightChecks}
          isUpgrade={isUpgrade}
        />
      )}
    </ConfigurationPage>
  );
}

function ToolInstallationSummary({
  clusterId,
  tool,
  charts,
}: {
  clusterId: string;
  tool: ClusterTool;
  charts: {
    namespace: string;
    releaseName?: string;
    chartName: string;
    chartVersion: string;
  }[];
}) {
  return (
    <>
      <InstallationDestination
        clusterId={clusterId}
        manager="Tools"
        releases={charts.map((chart) => ({
          namespace: chart.namespace,
          releaseName: chart.releaseName ?? chart.chartName,
          version: chart.chartVersion,
        }))}
      />
      {tool.charts.length === 1 ? (
        <CatalogOfferingSupport
          chart={tool.charts[0].chartName}
          repository={tool.charts[0].repoUrl}
          version={charts[0]?.chartVersion}
          detail
        />
      ) : (
        <OfferingSupport detail />
      )}
    </>
  );
}

function useInitialToolPreset(names: string[], selected: string) {
  return useState(() =>
    names.includes(selected)
      ? selected
      : names.includes("default")
        ? "default"
        : (names[0] ?? ""),
  );
}
