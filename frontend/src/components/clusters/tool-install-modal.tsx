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
  const order = ["Scaling", "Storage", "Resources", "Networking", "General"];
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

export function ToolInstallModal({
  tool,
  clusterId,
  preset,
  onConfirm,
  onClose,
  installing,
  confirmDecision,
}: ToolInstallModalProps) {
  const fields = tool.formSchema?.fields ?? EMPTY_TOOL_FIELDS;
  const hasForm = fields.length > 0;
  const [mode, setMode] = useState<EditorMode>(hasForm ? "form" : "yaml");
  // The preset is an install-time choice, so it lives here rather than on the
  // card: on the card it rendered next to every tool (installed ones included),
  // reading as a per-tool environment switch instead of "which chart values to
  // install with". `preset` seeds it from the cluster's environment. The chart
  // preview below keys on this, so switching presets re-previews live.
  const presetNames = Object.keys(tool.presets);
  const [selectedPreset, setSelectedPreset] = useState(() =>
    presetNames.includes(preset)
      ? preset
      : presetNames.includes("default")
        ? "default"
        : (presetNames[0] ?? ""),
  );

  // Only operator edits override the selected preset. Schema display defaults
  // must never silently replace development/production sizing.
  const [overrideValues, setOverrideValues] = useState<Record<string, unknown>>(
    {},
  );
  const [yamlText, setYamlText] = useState("");
  const [yamlError, setYamlError] = useState<string | null>(null);

  // Chart metadata (name/version/namespace) for the header.
  const { data: preview, isLoading } = useQuery({
    queryKey: queryKeys.tools.preview(tool.slug, clusterId, selectedPreset),
    queryFn: () =>
      previewToolInstall(tool.slug, {
        cluster_id: clusterId,
        preset: selectedPreset,
      }),
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
    enabled: mode === "review" && !yamlError,
  });

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
      title={`Install ${tool.name}`}
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
          disabled={isLoading || !!confirmBlockedReason || !!yamlError}
          disabledReason={confirmBlockedReason}
        />
      }
    >
      <PresetSelector
        names={presetNames}
        value={selectedPreset}
        onChange={setSelectedPreset}
      />

      {mode === "form" ? (
        <ToolSettingsEditor
          groups={groups}
          overrides={overrideValues}
          presetValues={presetValues}
          onChange={(field, value) =>
            setOverrideValues((previous) =>
              withPath(previous, field.path, coerce(field, value)),
            )
          }
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
}: {
  hasForm: boolean;
  mode: EditorMode;
  onModeChange: (mode: EditorMode) => void;
  onClose: () => void;
  onConfirm: () => void;
  installing?: boolean;
  disabled: boolean;
  disabledReason?: string;
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
          Install
        </ActionButton>
      </div>
    </div>
  );
}

function PresetSelector({
  names,
  value,
  onChange,
}: {
  names: string[];
  value: string;
  onChange: (value: string) => void;
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
        Sizing and replica defaults for this install. Defaults to the
        cluster&apos;s environment.
      </p>
    </div>
  );
}

function ToolSettingsEditor({
  groups,
  overrides,
  presetValues,
  onChange,
  onStorageClassChange,
}: {
  groups: Array<[string, ToolFormField[]]>;
  overrides: Record<string, unknown>;
  presetValues: Record<string, unknown>;
  onChange: (field: ToolFormField, value: string) => void;
  onStorageClassChange: (path: string, value: string) => void;
}) {
  return (
    <div className="space-y-6">
      <p className="text-xs text-muted-foreground">
        Configure the common settings below, or switch to{" "}
        <span className="font-medium">Edit YAML</span> for full control.
        Anything you leave at its default is taken from the chart.
      </p>
      {groups.map(([group, fields]) => (
        <section key={group} className="space-y-3">
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            {group}
          </h3>
          <div className="space-y-3">
            {fields.map((field) => (
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
                        valueAtPath(overrides, field.storageClassPath) ?? "",
                      )
                    : ""
                }
                onChange={(value) => onChange(field, value)}
                onClassChange={(value) =>
                  field.storageClassPath &&
                  onStorageClassChange(field.storageClassPath, value)
                }
              />
            ))}
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
        Server-validated effective values after distribution defaults, the
        selected preset, and your overrides are merged. Each release is shown
        separately in installation order.
      </p>
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
}: {
  field: ToolFormField;
  value: string;
  classValue: string;
  onChange: (v: string) => void;
  onClassChange: (v: string) => void;
}) {
  const inputId = useId();
  return (
    <div className="grid sm:grid-cols-[minmax(0,1fr)_220px] items-center gap-3">
      <div>
        <label htmlFor={inputId} className="text-sm text-foreground">
          {field.label}
        </label>
        {field.help && (
          <p className="text-xs text-muted-foreground mt-0.5">{field.help}</p>
        )}
        <p className="text-[10px] text-muted-foreground/70 font-mono mt-0.5">
          {field.path}
        </p>
      </div>
      {field.type === "boolean" ? (
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
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={field.placeholder}
          className="h-8"
        />
      )}
    </div>
  );
}
