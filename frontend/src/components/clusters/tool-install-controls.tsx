import { BareButton } from "@/components/form/bare-button";
import { ActionButton } from "@/components/ui/action-button";
import { Select } from "@/components/ui/select";
import {
  AlertTriangle,
  FileCode2,
  GitCompare,
  SlidersHorizontal,
} from "lucide-react";
import { type ReactNode } from "react";
export type EditorMode = "form" | "yaml" | "review";

export function ToolInstallFooter({
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

export function PresetSelector({
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

export function ToolYamlEditor({
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

export function ModeButton({
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
    <BareButton
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
    </BareButton>
  );
}
