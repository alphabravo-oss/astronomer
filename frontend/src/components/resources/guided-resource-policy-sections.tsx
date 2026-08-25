import {
  Field,
  type GuidedSectionProps,
} from "@/components/resources/guided-resource-fields";
import {
  keyValueText,
  manifestValue,
  parseKeyValueText,
  stringValue,
} from "@/components/resources/guided-resource-model";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

export function AutoscalingSection({ form }: GuidedSectionProps) {
  const { value, kind, errors, set, setNumber } = form;
  if (kind !== "HorizontalPodAutoscaler") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <div className="md:col-span-2">
        <h3 className="font-medium text-foreground">Autoscaling</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Scale a workload between safe bounds using average CPU utilization.
        </p>
      </div>
      <Field label="Target kind">
        <Select
          value={stringValue(value, ["spec", "scaleTargetRef", "kind"])}
          onChange={(event) =>
            set(["spec", "scaleTargetRef", "kind"], event.target.value)
          }
        >
          <option value="Deployment">Deployment</option>
          <option value="StatefulSet">StatefulSet</option>
        </Select>
      </Field>
      <Field label="Target name" error={errors.scaleTarget}>
        <Input
          value={stringValue(value, ["spec", "scaleTargetRef", "name"])}
          onChange={(event) =>
            set(["spec", "scaleTargetRef", "name"], event.target.value)
          }
        />
      </Field>
      <Field label="Minimum replicas" error={errors.minReplicas}>
        <Input
          type="number"
          min={1}
          value={stringValue(value, ["spec", "minReplicas"])}
          onChange={(event) =>
            setNumber(["spec", "minReplicas"], event.target.value)
          }
        />
      </Field>
      <Field label="Maximum replicas" error={errors.maxReplicas}>
        <Input
          type="number"
          min={1}
          value={stringValue(value, ["spec", "maxReplicas"])}
          onChange={(event) =>
            setNumber(["spec", "maxReplicas"], event.target.value)
          }
        />
      </Field>
      <Field label="Target CPU utilization (%)">
        <Input
          type="number"
          min={1}
          max={100}
          value={stringValue(value, [
            "spec",
            "metrics",
            0,
            "resource",
            "target",
            "averageUtilization",
          ])}
          onChange={(event) =>
            setNumber(
              [
                "spec",
                "metrics",
                0,
                "resource",
                "target",
                "averageUtilization",
              ],
              event.target.value,
            )
          }
        />
      </Field>
    </section>
  );
}

export function StorageClaimSection({ form }: GuidedSectionProps) {
  const { value, kind, errors, set } = form;
  if (kind !== "PersistentVolumeClaim") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">
        Storage claim
      </h3>
      <Field label="Storage class">
        <Input
          value={stringValue(value, ["spec", "storageClassName"])}
          onChange={(event) =>
            set(["spec", "storageClassName"], event.target.value)
          }
        />
      </Field>
      <Field label="Requested capacity" error={errors.storage}>
        <Input
          value={stringValue(value, [
            "spec",
            "resources",
            "requests",
            "storage",
          ])}
          onChange={(event) =>
            set(
              ["spec", "resources", "requests", "storage"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Access mode">
        <Select
          value={stringValue(value, ["spec", "accessModes", 0])}
          onChange={(event) =>
            set(["spec", "accessModes", 0], event.target.value)
          }
        >
          <option value="ReadWriteOnce">ReadWriteOnce</option>
          <option value="ReadOnlyMany">ReadOnlyMany</option>
          <option value="ReadWriteMany">ReadWriteMany</option>
          <option value="ReadWriteOncePod">ReadWriteOncePod</option>
        </Select>
      </Field>
    </section>
  );
}

export function DisruptionBudgetSection({ form }: GuidedSectionProps) {
  const { value, kind, set } = form;
  if (kind !== "PodDisruptionBudget") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">
        Availability budget
      </h3>
      <Field label="Minimum available">
        <Input
          value={stringValue(value, ["spec", "minAvailable"])}
          onChange={(event) =>
            set(["spec", "minAvailable"], event.target.value)
          }
        />
      </Field>
      <Field label="Pod selector" description="One key=value pair per line.">
        <Textarea
          value={keyValueText(
            manifestValue(value, ["spec", "selector", "matchLabels"]),
          )}
          onChange={(event) =>
            set(
              ["spec", "selector", "matchLabels"],
              parseKeyValueText(event.target.value),
            )
          }
          rows={3}
        />
      </Field>
    </section>
  );
}

export function NetworkPolicySection({ form }: GuidedSectionProps) {
  const { value, kind, set, setNumber } = form;
  if (kind !== "NetworkPolicy") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <div className="md:col-span-2">
        <h3 className="font-medium text-foreground">Network policy</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Define the selected pods and a simple allowed ingress peer. Use YAML
          mode for compound peers or IP blocks.
        </p>
      </div>
      <Field label="Selected pods" description="One key=value pair per line.">
        <Textarea
          value={keyValueText(
            manifestValue(value, ["spec", "podSelector", "matchLabels"]),
          )}
          onChange={(event) =>
            set(
              ["spec", "podSelector", "matchLabels"],
              parseKeyValueText(event.target.value),
            )
          }
          rows={3}
        />
      </Field>
      <Field
        label="Allowed peer pods"
        description="One key=value pair per line."
      >
        <Textarea
          value={keyValueText(
            manifestValue(value, [
              "spec",
              "ingress",
              0,
              "from",
              0,
              "podSelector",
              "matchLabels",
            ]),
          )}
          onChange={(event) =>
            set(
              ["spec", "ingress", 0, "from", 0, "podSelector", "matchLabels"],
              parseKeyValueText(event.target.value),
            )
          }
          rows={3}
        />
      </Field>
      <Field label="Allowed ingress port">
        <Input
          type="number"
          min={1}
          max={65535}
          value={stringValue(value, ["spec", "ingress", 0, "ports", 0, "port"])}
          onChange={(event) =>
            setNumber(
              ["spec", "ingress", 0, "ports", 0, "port"],
              event.target.value,
            )
          }
        />
      </Field>
    </section>
  );
}
