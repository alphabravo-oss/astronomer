import type { ReactNode } from "react";

import type {
  KubernetesManifest,
  ManifestPath,
} from "@/components/resources/guided-resource-model";

export interface GuidedFormState {
  value: KubernetesManifest;
  kind: string;
  podPath: ManifestPath | null;
  containerPath: ManifestPath | null;
  errors: Record<string, string>;
  identityReadOnly: boolean;
  onChange: (value: KubernetesManifest) => void;
  doc: (path: ManifestPath, fallback: string) => string;
  set: (path: ManifestPath, value: unknown) => void;
  setNumber: (path: ManifestPath, value: string) => void;
}

export interface GuidedSectionProps {
  form: GuidedFormState;
}

export function Field({
  label,
  description,
  error,
  children,
}: {
  label: string;
  description?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <label className="space-y-1.5 text-sm">
      <span className="font-medium text-foreground">{label}</span>
      {description && (
        <span className="block text-xs leading-relaxed text-muted-foreground">
          {description}
        </span>
      )}
      {children}
      {error && (
        <span className="block text-xs text-status-error" role="alert">
          {error}
        </span>
      )}
    </label>
  );
}

export function CheckboxField({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex items-center gap-2 text-sm text-foreground">
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
        className="h-4 w-4 rounded border-border"
      />
      {label}
    </label>
  );
}
