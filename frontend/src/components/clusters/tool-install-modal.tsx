import { ActionButton } from "@/components/ui/action-button";
import { ModalShell } from "@/components/ui/modal-shell";
import { previewToolInstall } from "@/lib/api/tools";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import { queryKeys } from "@/lib/query-keys";
import { toastWarning } from "@/lib/toast";
import type { ClusterTool, ToolFormField } from "@/types";
import { useQuery } from "@tanstack/react-query";
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

interface ToolInstallModalProps {
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

export function ToolInstallModal(props: ToolInstallModalProps) {
  if (props.action === "upgrade" && props.initialValuesYaml === undefined) {
    return (
      <ModalShell
        title={`Configure ${props.tool.name}`}
        onClose={props.onClose}
        size="lg"
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
      </ModalShell>
    );
  }
  return <ToolInstallModalEditor {...props} />;
}

function ToolInstallModalEditor({
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
}: ToolInstallModalProps) {
  const isUpgrade = action === "upgrade";
  const fields = tool.formSchema?.fields ?? EMPTY_TOOL_FIELDS;
  const hasForm = fields.length > 0;
  const [mode, setMode] = useState<EditorMode>(hasForm ? "form" : "yaml");
  // Presets belong to the install editor; changing one refreshes its preview.
  const presetNames = useMemo(() => Object.keys(tool.presets), [tool.presets]);
  const [selectedPreset, setSelectedPreset] = useState(() =>
    presetNames.includes(initialPreset ?? preset)
      ? (initialPreset ?? preset)
      : presetNames.includes("default")
        ? "default"
        : (presetNames[0] ?? ""),
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

  // Chart metadata (name/version/namespace) for the header.
  const { data: preview, isLoading } = useQuery({
    queryKey: queryKeys.tools.preview(
      tool.slug,
      clusterId,
      selectedPreset,
      isUpgrade ? (initialValuesYaml ?? "") : "",
    ),
    queryFn: () =>
      previewToolInstall(tool.slug, {
        cluster_id: clusterId,
        preset: selectedPreset,
        values_override:
          isUpgrade && initialValuesYaml ? initialValuesYaml : undefined,
      }),
    enabled: !isUpgrade || initialValuesYaml !== undefined,
  });
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
    <ModalShell
      title={`${isUpgrade ? "Configure" : "Install"} ${tool.name}`}
      subtitle={
        isUpgrade
          ? "Review saved values, change common settings or edit the complete YAML, then preview the exact release plan."
          : "Choose a preset, adjust common settings, and review the exact release plan before installation."
      }
      onClose={onClose}
      size="lg"
      panelClassName="max-w-2xl max-h-[88vh] bg-popover flex flex-col overflow-hidden"
      bodyClassName="flex-1 overflow-y-auto"
      footerClassName="bg-muted/30"
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
      <PresetSelector
        names={presetNames}
        value={selectedPreset}
        onChange={setSelectedPreset}
        isUpgrade={isUpgrade}
      />

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
    </ModalShell>
  );
}
