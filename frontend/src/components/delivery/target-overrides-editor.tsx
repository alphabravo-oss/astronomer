import { textareaClass } from "@/components/delivery/shared";

export interface TargetOverridesInput {
  helm_values?: Record<string, unknown>;
  patches?: string[];
}

export function targetOverridesFromForm(form: FormData): TargetOverridesInput {
  const helmText = String(form.get("override_helm_values") ?? "").trim();
  const patchesText = String(form.get("override_patches") ?? "").trim();
  const helmValues = helmText
    ? (JSON.parse(helmText) as unknown)
    : undefined;
  if (
    helmValues !== undefined &&
    (helmValues === null || Array.isArray(helmValues) || typeof helmValues !== "object")
  ) {
    throw new Error("Helm value overrides must be a JSON object.");
  }
  const patches = patchesText
    ? patchesText.split(/^---\s*$/m).map((patch) => patch.trim()).filter(Boolean)
    : undefined;
  return {
    ...(helmValues ? { helm_values: helmValues as Record<string, unknown> } : {}),
    ...(patches?.length ? { patches } : {}),
  };
}

export function TargetOverridesEditor({
  value,
  digest,
}: {
  value?: TargetOverridesInput;
  digest?: string;
}) {
  return (
    <fieldset className="space-y-4 rounded-md border border-border p-4">
      <legend className="px-1 text-sm font-medium">Per-target customization</legend>
      <p className="text-xs text-muted-foreground">
        Helm values are deep-merged; Kubernetes patches are appended in order. The server
        rejects fields that do not match the selected renderer and freezes the result into
        the rollout digest.
      </p>
      <label className="block space-y-1.5 text-sm">
        <span className="font-medium">Helm values (JSON object)</span>
        <textarea
          name="override_helm_values"
          className={textareaClass}
          defaultValue={value?.helm_values ? JSON.stringify(value.helm_values, null, 2) : ""}
          placeholder={'{"replicaCount": 3}'}
        />
      </label>
      <label className="block space-y-1.5 text-sm">
        <span className="font-medium">Kubernetes patches (YAML, separated by ---)</span>
        <textarea
          name="override_patches"
          className={textareaClass}
          defaultValue={value?.patches?.join("\n---\n") ?? ""}
          placeholder={'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: example'}
        />
      </label>
      {digest && <p className="break-all font-mono text-xs text-muted-foreground">Override digest: {digest}</p>}
    </fieldset>
  );
}
