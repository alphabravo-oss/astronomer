import { useId, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import * as yaml from "js-yaml";
import { previewToolInstall } from "@/lib/api/tools";
import { queryKeys } from "@/lib/query-keys";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  AlertTriangle,
  FileCode2,
  GitCompare,
  RotateCcw,
  SlidersHorizontal,
} from "lucide-react";
import { permissionDeniedReason } from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import type { ClusterTool, ToolFormField } from "@/types";
import { toastWarning } from "@/lib/toast";
import { previewToolFieldValues } from "./tool-values";
import { buildYamlDiff } from "@/components/ui/yaml-apply-preview";

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

type EditorMode = "form" | "yaml" | "review";

function parseOverride(raw: string): Record<string, unknown> | null {
  if (!raw.trim()) return {};
  try {
    const parsed = yaml.load(raw);
    if (!parsed) return {};
    return typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

function valueAtPath(root: Record<string, unknown>, path: string): unknown {
  let value: unknown = root;
  for (const segment of path.split(".")) {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return undefined;
    }
    value = (value as Record<string, unknown>)[segment];
  }
  return value;
}

// setPath writes value into a nested object at a dot-path, creating intermediate
// objects as needed: setPath({}, "a.b.c", 1) => { a: { b: { c: 1 } } }.
function setPath(root: Record<string, unknown>, path: string, value: unknown) {
  const parts = path.split(".");
  let node = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i];
    if (typeof node[k] !== "object" || node[k] === null) node[k] = {};
    node = node[k] as Record<string, unknown>;
  }
  node[parts[parts.length - 1]] = value;
}

function withPath(
  root: Record<string, unknown>,
  path: string,
  value: unknown,
): Record<string, unknown> {
  const next = structuredClone(root);
  setPath(next, path, value);
  return next;
}

function withoutPath(
  root: Record<string, unknown>,
  path: string,
): Record<string, unknown> {
  const next = structuredClone(root);
  const parts = path.split(".");
  const parents: Array<[Record<string, unknown>, string]> = [];
  let node = next;
  for (const part of parts.slice(0, -1)) {
    const child = node[part];
    if (!child || typeof child !== "object" || Array.isArray(child)) {
      return next;
    }
    parents.push([node, part]);
    node = child as Record<string, unknown>;
  }
  delete node[parts.at(-1)!];
  for (const [parent, key] of parents.reverse()) {
    const child = parent[key];
    if (
      child &&
      typeof child === "object" &&
      !Array.isArray(child) &&
      Object.keys(child).length === 0
    ) {
      delete parent[key];
    }
  }
  return next;
}

function coerce(field: ToolFormField, raw: string): unknown {
  if (field.type === "number") {
    const n = Number(raw);
    return Number.isFinite(n) ? n : raw;
  }
  if (field.type === "boolean") return raw === "true";
  return raw;
}

function groupFields(
  fields: ToolFormField[],
): Array<[string, ToolFormField[]]> {
  const order = [
    "General",
    "Base CRDs",
    "Control plane",
    "Scaling",
    "Storage",
    "Networking",
    "Pipeline",
    "Scanners",
    "Runtime",
    "Security",
    "Injection",
    "Telemetry",
    "Monitoring",
    "Scheduling",
    "Resources",
  ];
  const byGroup = new Map<string, ToolFormField[]>();
  for (const f of fields) {
    const g = f.group || "General";
    if (!byGroup.has(g)) byGroup.set(g, []);
    byGroup.get(g)!.push(f);
  }
  return Array.from(byGroup.entries()).sort(
    (a, b) => (order.indexOf(a[0]) + 1 || 99) - (order.indexOf(b[0]) + 1 || 99),
  );
}

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
  // The preset is an install-time choice, so it lives here rather than on the
  // card: on the card it rendered next to every tool (installed ones included),
  // reading as a per-tool environment switch instead of "which chart values to
  // install with". `preset` seeds it from the cluster's environment. The chart
  // preview below keys on this, so switching presets re-previews live.
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

  const effectivePreview = useQuery({
    queryKey: queryKeys.tools.preview(
      tool.slug,
      clusterId,
      selectedPreset,
      overrideYaml,
    ),
    queryFn: () =>
      previewToolInstall(tool.slug, {
        cluster_id: clusterId,
        preset: selectedPreset,
        values_override: overrideYaml || undefined,
      }),
    enabled:
      mode === "review" &&
      !yamlError &&
      (!isUpgrade || initialValuesYaml !== undefined),
  });

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

function ToolInstallFooter({
  hasForm,
  mode,
  onModeChange,
  onClose,
  onConfirm,
  installing,
  disabled,
  disabledReason,
  action,
}: {
  hasForm: boolean;
  mode: EditorMode;
  onModeChange: (mode: EditorMode) => void;
  onClose: () => void;
  onConfirm: () => void;
  installing?: boolean;
  disabled: boolean;
  disabledReason?: string;
  action: "install" | "upgrade";
}) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="inline-flex rounded-md border border-border bg-background p-1">
        {hasForm && (
          <ModeButton
            active={mode === "form"}
            onClick={() => onModeChange("form")}
            icon={<SlidersHorizontal className="h-3.5 w-3.5" />}
            label="Settings"
          />
        )}
        <ModeButton
          active={mode === "yaml"}
          onClick={() => onModeChange("yaml")}
          icon={<FileCode2 className="h-3.5 w-3.5" />}
          label="YAML"
        />
        <ModeButton
          active={mode === "review"}
          onClick={() => onModeChange("review")}
          icon={<GitCompare className="h-3.5 w-3.5" />}
          label="Review"
        />
      </div>
      <div className="flex items-center gap-2">
        <ActionButton onClick={onClose}>Cancel</ActionButton>
        <ActionButton
          intent="primary"
          onClick={onConfirm}
          disabled={installing || disabled}
          disabledReason={disabledReason}
          loading={installing}
        >
          {action === "upgrade" ? "Apply changes" : "Install"}
        </ActionButton>
      </div>
    </div>
  );
}

function PresetSelector({
  names,
  value,
  onChange,
  isUpgrade,
}: {
  names: string[];
  value: string;
  onChange: (value: string) => void;
  isUpgrade: boolean;
}) {
  return (
    <div className="mb-5 space-y-1.5">
      <label
        htmlFor="tool-preset"
        className="text-sm font-medium text-foreground"
      >
        Preset
      </label>
      <Select
        id="tool-preset"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {(names.length ? names : [""]).map((name) => (
          <option key={name} value={name}>
            {name
              ? name.charAt(0).toUpperCase() + name.slice(1)
              : "Chart defaults"}
          </option>
        ))}
      </Select>
      <p className="text-xs text-muted-foreground">
        Sizing and replica defaults for this{" "}
        {isUpgrade ? "configuration" : "install"}.
        {isUpgrade
          ? " Saved values remain until you reset or change them."
          : " Defaults to the cluster's environment."}
      </p>
    </div>
  );
}

function ToolSettingsEditor({
  groups,
  overrides,
  presetValues,
  onChange,
  onReset,
  onStorageClassChange,
  editedPaths,
  isUpgrade,
}: {
  groups: Array<[string, ToolFormField[]]>;
  overrides: Record<string, unknown>;
  presetValues: Record<string, unknown>;
  onChange: (field: ToolFormField, value: string) => void;
  onReset: (field: ToolFormField) => void;
  onStorageClassChange: (path: string, value: string) => void;
  editedPaths: Set<string>;
  isUpgrade: boolean;
}) {
  const effectiveValues = useMemo(() => {
    const values = { ...presetValues };
    for (const [, groupFields] of groups) {
      for (const field of groupFields) {
        const override = valueAtPath(overrides, field.path);
        if (override !== undefined) values[field.path] = override;
      }
    }
    return values;
  }, [groups, overrides, presetValues]);

  return (
    <div className="space-y-6">
      <p className="text-xs text-muted-foreground">
        Configure common settings below, or switch to YAML for the complete
        chart surface. Each field shows whether it comes from the preset, the
        saved installation, or this edit.
      </p>
      {groups.map(([group, fields]) => (
        <section key={group} className="space-y-3">
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            {group}
          </h3>
          <div className="space-y-3">
            {fields
              .filter((field) => {
                if (!field.showWhen) return true;
                const value = effectiveValues[field.showWhen.path];
                return String(value) === field.showWhen.equals;
              })
              .map((field) => {
                const hasOverride =
                  valueAtPath(overrides, field.path) !== undefined;
                const hasPreset = presetValues[field.path] !== undefined;
                const changed = editedPaths.has(field.path);
                const source = !hasOverride
                  ? hasPreset
                    ? "Preset"
                    : "Chart default"
                  : changed
                    ? "Changed"
                    : isUpgrade
                      ? "Saved"
                      : "Custom";
                return (
                  <FormFieldRow
                    key={field.path}
                    field={field}
                    value={String(
                      valueAtPath(overrides, field.path) ??
                        presetValues[field.path] ??
                        field.default ??
                        "",
                    )}
                    classValue={
                      field.storageClassPath
                        ? String(
                            valueAtPath(overrides, field.storageClassPath) ??
                              "",
                          )
                        : ""
                    }
                    source={source}
                    canReset={hasOverride}
                    onReset={() => onReset(field)}
                    onChange={(value) => onChange(field, value)}
                    onClassChange={(value) =>
                      field.storageClassPath &&
                      onStorageClassChange(field.storageClassPath, value)
                    }
                  />
                );
              })}
          </div>
        </section>
      ))}
    </div>
  );
}

function ToolYamlEditor({
  value,
  error,
  onChange,
  onBlur,
}: {
  value: string;
  error: string | null;
  onChange: (value: string) => void;
  onBlur: () => void;
}) {
  return (
    <div className="space-y-1.5">
      <label
        className="text-sm font-medium text-foreground"
        htmlFor="tool-values-yaml"
      >
        Values override (YAML)
      </label>
      <p className="text-xs text-muted-foreground">
        Merged on top of the chart defaults and the selected preset.
      </p>
      <textarea
        id="tool-values-yaml"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onBlur={onBlur}
        rows={18}
        placeholder={
          "# e.g.\nreplicas: 2\nresources:\n  requests:\n    cpu: 100m"
        }
        className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
          placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring resize-none"
      />
      {error && (
        <p
          role="alert"
          className="flex items-center gap-1.5 text-xs text-status-error"
        >
          <AlertTriangle className="h-3.5 w-3.5" />
          {error}
        </p>
      )}
    </div>
  );
}

function ModeButton({
  active,
  onClick,
  icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: ReactNode;
  label: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={`inline-flex items-center gap-1 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors ${
        active
          ? "bg-muted text-foreground shadow-xs"
          : "text-muted-foreground hover:text-foreground"
      }`}
    >
      {icon}
      {label}
    </button>
  );
}

function ToolValuesReview({
  baseline,
  effective,
  loading,
  error,
  checks,
  isUpgrade,
}: {
  baseline: Array<{
    chartName: string;
    chartVersion: string;
    releaseName?: string;
    namespace: string;
    valuesYaml: string;
  }>;
  effective: Array<{
    chartName: string;
    chartVersion: string;
    releaseName?: string;
    namespace: string;
    valuesYaml: string;
  }>;
  loading: boolean;
  error: Error | null;
  checks: Array<{
    code: string;
    status: "pass" | "warn" | "block";
    message: string;
  }>;
  isUpgrade: boolean;
}) {
  if (loading) {
    return (
      <p className="text-sm text-muted-foreground">
        Building server-validated preview…
      </p>
    );
  }
  if (error) {
    return (
      <p role="alert" className="text-sm text-status-error">
        Could not build the effective values preview: {error.message}
      </p>
    );
  }
  return (
    <div className="space-y-5">
      <p className="text-xs text-muted-foreground">
        {isUpgrade
          ? "Changes from the saved installation to the proposed server-validated values."
          : "Changes from the selected preset to the proposed server-validated values."}{" "}
        Each release is shown separately in installation order.
      </p>
      {checks.length > 0 && (
        <section aria-label="Preflight checks" className="space-y-2">
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Preflight
          </h3>
          {checks.map((check) => (
            <div
              key={check.code}
              className={`rounded-md border px-3 py-2 text-xs ${
                check.status === "block"
                  ? "border-status-error/30 bg-status-error/5 text-status-error"
                  : check.status === "warn"
                    ? "border-status-warning/30 bg-status-warning/5 text-status-warning"
                    : "border-status-success/30 bg-status-success/5 text-status-success"
              }`}
            >
              <span className="font-semibold capitalize">{check.status}</span>
              {" — "}
              {check.message}
            </div>
          ))}
        </section>
      )}
      {effective.map((chart, index) => {
        const before = baseline[index]?.valuesYaml ?? "";
        const diff = buildYamlDiff(before, chart.valuesYaml);
        return (
          <section
            key={`${chart.namespace}/${chart.releaseName ?? chart.chartName}`}
            className="overflow-hidden rounded-lg border border-border"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/30 px-3 py-2">
              <div>
                <h3 className="text-sm font-medium text-foreground">
                  {index + 1}. {chart.releaseName ?? chart.chartName}
                </h3>
                <p className="text-[11px] text-muted-foreground">
                  {chart.chartName}@{chart.chartVersion} · {chart.namespace}
                </p>
              </div>
              <span className="text-xs tabular-nums text-muted-foreground">
                +{diff.added} / -{diff.removed}
              </span>
            </div>
            <pre className="max-h-64 overflow-auto p-3 text-xs leading-5">
              {diff.lines.map((line) => (
                <div
                  key={line.key}
                  className={`min-w-max font-mono ${
                    line.type === "add"
                      ? "bg-status-success/10 text-status-success"
                      : line.type === "remove"
                        ? "bg-status-error/10 text-status-error"
                        : "text-muted-foreground"
                  }`}
                >
                  <span className="inline-block w-4 select-none">
                    {line.type === "add"
                      ? "+"
                      : line.type === "remove"
                        ? "-"
                        : " "}
                  </span>
                  {line.text}
                </div>
              ))}
            </pre>
          </section>
        );
      })}
    </div>
  );
}

function FormFieldRow({
  field,
  value,
  classValue,
  onChange,
  onClassChange,
  source,
  canReset,
  onReset,
}: {
  field: ToolFormField;
  value: string;
  classValue: string;
  onChange: (v: string) => void;
  onClassChange: (v: string) => void;
  source: string;
  canReset: boolean;
  onReset: () => void;
}) {
  const inputId = useId();
  return (
    <div
      className={`grid gap-3 ${
        field.type === "multiline"
          ? "items-start"
          : "items-center sm:grid-cols-[minmax(0,1fr)_220px]"
      }`}
    >
      <div>
        <label htmlFor={inputId} className="text-sm text-foreground">
          {field.label}
        </label>
        {field.help && (
          <p className="text-xs text-muted-foreground mt-0.5">{field.help}</p>
        )}
        <div className="mt-0.5 flex flex-wrap items-center gap-2 text-[10px] text-muted-foreground/70">
          <span className="font-mono">{field.path}</span>
          <span className="rounded bg-muted px-1.5 py-0.5 font-medium uppercase tracking-wide">
            {source}
          </span>
          {canReset && (
            <button
              type="button"
              onClick={onReset}
              className="inline-flex items-center gap-1 text-primary hover:underline"
              aria-label={`Reset ${field.label} to preset`}
            >
              <RotateCcw className="h-2.5 w-2.5" /> Reset
            </button>
          )}
        </div>
      </div>
      {field.type === "multiline" ? (
        <textarea
          id={inputId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          rows={8}
          spellCheck={false}
          className="w-full resize-y rounded-md border border-border bg-background px-3 py-2 font-mono text-xs leading-5 placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring"
        />
      ) : field.type === "boolean" ? (
        <label className="inline-flex items-center gap-2 justify-self-end cursor-pointer">
          <input
            id={inputId}
            aria-label={field.label}
            type="checkbox"
            checked={value === "true"}
            onChange={(e) => onChange(e.target.checked ? "true" : "false")}
            className="h-4 w-4 rounded-sm border-border"
          />
          <span className="text-xs text-muted-foreground">
            {value === "true" ? "Enabled" : "Disabled"}
          </span>
        </label>
      ) : field.type === "select" ? (
        <Select
          id={inputId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="h-8"
        >
          {(field.options ?? []).map((opt) => (
            <option key={opt} value={opt}>
              {opt}
            </option>
          ))}
        </Select>
      ) : field.type === "storage" ? (
        <div className="flex gap-2">
          <Input
            id={inputId}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.placeholder || "10Gi"}
            className="h-8 w-24"
          />
          <Input
            aria-label={`${field.label} storage class`}
            value={classValue}
            onChange={(e) => onClassChange(e.target.value)}
            placeholder="storageClass"
            className="h-8 flex-1"
          />
        </div>
      ) : (
        <Input
          id={inputId}
          type={field.type === "number" ? "number" : "text"}
          min={field.type === "number" ? field.minimum : undefined}
          max={field.type === "number" ? field.maximum : undefined}
          step={field.type === "number" ? field.step : undefined}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          className="h-8"
        />
      )}
    </div>
  );
}
