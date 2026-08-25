import {
  CheckboxField,
  Field,
  type GuidedSectionProps,
} from "@/components/resources/guided-resource-fields";
import {
  booleanValue,
  envText,
  keyValueText,
  manifestValue,
  parseEnvText,
  parseKeyValueText,
  stringValue,
  updateManifest,
} from "@/components/resources/guided-resource-model";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

function MetadataFields({ form }: GuidedSectionProps) {
  const { value, set } = form;
  return (
    <>
      <Field label="Labels" description="One key=value pair per line.">
        <Textarea
          value={keyValueText(manifestValue(value, ["metadata", "labels"]))}
          onChange={(event) =>
            set(["metadata", "labels"], parseKeyValueText(event.target.value))
          }
          rows={4}
        />
      </Field>
      <Field label="Annotations" description="One key=value pair per line.">
        <Textarea
          value={keyValueText(
            manifestValue(value, ["metadata", "annotations"]),
          )}
          onChange={(event) =>
            set(
              ["metadata", "annotations"],
              parseKeyValueText(event.target.value),
            )
          }
          rows={4}
        />
      </Field>
    </>
  );
}

function WorkloadRuntimeFields({ form }: GuidedSectionProps) {
  const { value, podPath, containerPath, set } = form;
  if (!containerPath || !podPath) return null;
  return (
    <>
      <Field label="Environment" description="One NAME=value pair per line.">
        <Textarea
          value={envText(manifestValue(value, [...containerPath, "env"]))}
          onChange={(event) =>
            set([...containerPath, "env"], parseEnvText(event.target.value))
          }
          rows={4}
        />
      </Field>
      <Field label="Service account">
        <Input
          value={stringValue(value, [...podPath, "serviceAccountName"])}
          onChange={(event) =>
            set([...podPath, "serviceAccountName"], event.target.value)
          }
        />
      </Field>
      <Field label="CPU request">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "resources",
            "requests",
            "cpu",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "resources", "requests", "cpu"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Memory request">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "resources",
            "requests",
            "memory",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "resources", "requests", "memory"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="CPU limit">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "resources",
            "limits",
            "cpu",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "resources", "limits", "cpu"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Memory limit">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "resources",
            "limits",
            "memory",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "resources", "limits", "memory"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Readiness path">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "readinessProbe",
            "httpGet",
            "path",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "readinessProbe", "httpGet", "path"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Liveness path">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "livenessProbe",
            "httpGet",
            "path",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "livenessProbe", "httpGet", "path"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="ConfigMap environment source">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "envFrom",
            0,
            "configMapRef",
            "name",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "envFrom", 0, "configMapRef", "name"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Secret environment source">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "envFrom",
            1,
            "secretRef",
            "name",
          ])}
          onChange={(event) =>
            set(
              [...containerPath, "envFrom", 1, "secretRef", "name"],
              event.target.value,
            )
          }
        />
      </Field>
    </>
  );
}

function WorkloadStorageFields({ form }: GuidedSectionProps) {
  const { value, podPath, containerPath, onChange, set } = form;
  if (!containerPath || !podPath) return null;
  return (
    <>
      <Field label="PVC claim">
        <Input
          value={stringValue(value, [
            ...podPath,
            "volumes",
            0,
            "persistentVolumeClaim",
            "claimName",
          ])}
          onChange={(event) => {
            let next = updateManifest(
              value,
              [...podPath, "volumes", 0, "name"],
              "data",
            );
            next = updateManifest(
              next,
              [...podPath, "volumes", 0, "persistentVolumeClaim", "claimName"],
              event.target.value,
            );
            onChange(next);
          }}
        />
      </Field>
      <Field label="Volume mount path">
        <Input
          value={stringValue(value, [
            ...containerPath,
            "volumeMounts",
            0,
            "mountPath",
          ])}
          onChange={(event) => {
            let next = updateManifest(
              value,
              [...containerPath, "volumeMounts", 0, "name"],
              "data",
            );
            next = updateManifest(
              next,
              [...containerPath, "volumeMounts", 0, "mountPath"],
              event.target.value,
            );
            onChange(next);
          }}
        />
      </Field>
      <Field label="Node selector" description="One key=value pair per line.">
        <Textarea
          value={keyValueText(
            manifestValue(value, [...podPath, "nodeSelector"]),
          )}
          onChange={(event) =>
            set(
              [...podPath, "nodeSelector"],
              parseKeyValueText(event.target.value),
            )
          }
          rows={3}
        />
      </Field>
    </>
  );
}

function SecurityContextFields({ form }: GuidedSectionProps) {
  const { value, podPath, containerPath, set } = form;
  if (!containerPath || !podPath) return null;
  return (
    <div className="space-y-3 rounded-md border border-border p-3">
      <p className="text-sm font-medium text-foreground">Security context</p>
      <CheckboxField
        label="Run as non-root"
        checked={booleanValue(value, [
          ...podPath,
          "securityContext",
          "runAsNonRoot",
        ])}
        onChange={(checked) =>
          set([...podPath, "securityContext", "runAsNonRoot"], checked)
        }
      />
      <CheckboxField
        label="Read-only root filesystem"
        checked={booleanValue(value, [
          ...containerPath,
          "securityContext",
          "readOnlyRootFilesystem",
        ])}
        onChange={(checked) =>
          set(
            [...containerPath, "securityContext", "readOnlyRootFilesystem"],
            checked,
          )
        }
      />
      <CheckboxField
        label="Allow privilege escalation"
        checked={booleanValue(value, [
          ...containerPath,
          "securityContext",
          "allowPrivilegeEscalation",
        ])}
        onChange={(checked) =>
          set(
            [...containerPath, "securityContext", "allowPrivilegeEscalation"],
            checked,
          )
        }
      />
    </div>
  );
}

function RolloutStrategyFields({ form }: GuidedSectionProps) {
  const { value, kind, set } = form;
  if (kind !== "Deployment") return null;
  return (
    <div className="space-y-3 rounded-md border border-border p-3">
      <p className="text-sm font-medium text-foreground">Rollout strategy</p>
      <Field label="Strategy">
        <Select
          value={stringValue(value, ["spec", "strategy", "type"])}
          onChange={(event) =>
            set(["spec", "strategy", "type"], event.target.value)
          }
        >
          <option value="RollingUpdate">RollingUpdate</option>
          <option value="Recreate">Recreate</option>
        </Select>
      </Field>
      <Field label="Max unavailable">
        <Input
          value={stringValue(value, [
            "spec",
            "strategy",
            "rollingUpdate",
            "maxUnavailable",
          ])}
          onChange={(event) =>
            set(
              ["spec", "strategy", "rollingUpdate", "maxUnavailable"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Max surge">
        <Input
          value={stringValue(value, [
            "spec",
            "strategy",
            "rollingUpdate",
            "maxSurge",
          ])}
          onChange={(event) =>
            set(
              ["spec", "strategy", "rollingUpdate", "maxSurge"],
              event.target.value,
            )
          }
        />
      </Field>
    </div>
  );
}

export function AdvancedFieldsSection({ form }: GuidedSectionProps) {
  return (
    <details className="rounded-lg border border-border bg-card p-4">
      <summary className="cursor-pointer font-medium text-foreground">
        Advanced fields
      </summary>
      <div className="mt-4 grid gap-4 md:grid-cols-2">
        <MetadataFields form={form} />
        <WorkloadRuntimeFields form={form} />
        <WorkloadStorageFields form={form} />
        <SecurityContextFields form={form} />
        <RolloutStrategyFields form={form} />
      </div>
    </details>
  );
}
