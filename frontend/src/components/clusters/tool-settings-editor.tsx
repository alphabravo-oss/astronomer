import { BareButton } from "@/components/form/bare-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { ToolFormField } from "@/types";
import { RotateCcw } from "lucide-react";
import { useId, useMemo } from "react";
import { valueAtPath } from "./tool-install-values";
export function ToolSettingsEditor({
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

export function FormFieldRow({
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
            <BareButton
              type="button"
              onClick={onReset}
              className="inline-flex items-center gap-1 text-primary hover:underline"
              aria-label={`Reset ${field.label} to preset`}
            >
              <RotateCcw className="h-2.5 w-2.5" /> Reset
            </BareButton>
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
