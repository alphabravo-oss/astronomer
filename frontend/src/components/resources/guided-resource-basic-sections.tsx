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

export function IdentitySection({ form }: GuidedSectionProps) {
  const { value, kind, errors, doc, set, identityReadOnly } = form;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <div className="md:col-span-2">
        <h3 className="font-medium text-foreground">Identity</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Kubernetes API version and kind come from the selected resource
          template.
        </p>
      </div>
      <Field
        label="Name"
        description={doc(
          ["metadata", "name"],
          "Unique DNS-compatible name for this resource.",
        )}
        error={errors.name}
      >
        <Input
          value={stringValue(value, ["metadata", "name"])}
          onChange={(event) => set(["metadata", "name"], event.target.value)}
          aria-invalid={!!errors.name}
          disabled={identityReadOnly}
        />
      </Field>
      {kind !== "Namespace" && (
        <Field
          label="Namespace"
          description={doc(
            ["metadata", "namespace"],
            "Namespace where the resource will be created.",
          )}
          error={errors.namespace}
        >
          <Input
            value={stringValue(value, ["metadata", "namespace"])}
            onChange={(event) =>
              set(["metadata", "namespace"], event.target.value)
            }
            aria-invalid={!!errors.namespace}
            disabled={identityReadOnly}
          />
        </Field>
      )}
    </section>
  );
}

export function PrimaryWorkloadSection({ form }: GuidedSectionProps) {
  const { value, kind, containerPath, podPath, errors, doc, set, setNumber } =
    form;
  if (!containerPath || !podPath) return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <div className="md:col-span-2">
        <h3 className="font-medium text-foreground">Workload</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Configure the primary container. Additional containers remain
          available in YAML mode.
        </p>
      </div>
      {!["DaemonSet", "Job", "CronJob"].includes(kind) && (
        <Field label="Replicas" description="Desired number of pods.">
          <Input
            type="number"
            min={0}
            value={stringValue(value, ["spec", "replicas"])}
            onChange={(event) =>
              setNumber(["spec", "replicas"], event.target.value)
            }
          />
        </Field>
      )}
      <Field label="Container name">
        <Input
          value={stringValue(value, [...containerPath, "name"])}
          onChange={(event) =>
            set([...containerPath, "name"], event.target.value)
          }
        />
      </Field>
      <Field
        label="Container image"
        description={doc(
          [...containerPath, "image"],
          "Registry image reference, preferably pinned by digest.",
        )}
        error={errors.image}
      >
        <Input
          value={stringValue(value, [...containerPath, "image"])}
          onChange={(event) =>
            set([...containerPath, "image"], event.target.value)
          }
          aria-invalid={!!errors.image}
        />
      </Field>
      <Field label="Container port" error={errors.containerPort}>
        <Input
          type="number"
          min={1}
          max={65535}
          value={stringValue(value, [
            ...containerPath,
            "ports",
            0,
            "containerPort",
          ])}
          onChange={(event) =>
            setNumber(
              [...containerPath, "ports", 0, "containerPort"],
              event.target.value,
            )
          }
          aria-invalid={!!errors.containerPort}
        />
      </Field>
      {kind === "CronJob" && (
        <Field label="Schedule" error={errors.schedule}>
          <Input
            value={stringValue(value, ["spec", "schedule"])}
            onChange={(event) => set(["spec", "schedule"], event.target.value)}
            aria-invalid={!!errors.schedule}
          />
        </Field>
      )}
    </section>
  );
}

export function ServiceSection({ form }: GuidedSectionProps) {
  const { value, kind, errors, set, setNumber } = form;
  if (kind !== "Service") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">Networking</h3>
      <Field label="Service type">
        <Select
          value={stringValue(value, ["spec", "type"])}
          onChange={(event) => set(["spec", "type"], event.target.value)}
        >
          <option value="ClusterIP">ClusterIP</option>
          <option value="NodePort">NodePort</option>
          <option value="LoadBalancer">LoadBalancer</option>
          <option value="ExternalName">ExternalName</option>
        </Select>
      </Field>
      <Field label="Port" error={errors.servicePort}>
        <Input
          type="number"
          min={1}
          max={65535}
          value={stringValue(value, ["spec", "ports", 0, "port"])}
          onChange={(event) =>
            setNumber(["spec", "ports", 0, "port"], event.target.value)
          }
        />
      </Field>
      <Field label="Target port">
        <Input
          value={stringValue(value, ["spec", "ports", 0, "targetPort"])}
          onChange={(event) =>
            set(["spec", "ports", 0, "targetPort"], event.target.value)
          }
        />
      </Field>
      <Field label="Pod selector" description="One key=value pair per line.">
        <Textarea
          value={keyValueText(manifestValue(value, ["spec", "selector"]))}
          onChange={(event) =>
            set(["spec", "selector"], parseKeyValueText(event.target.value))
          }
          rows={3}
        />
      </Field>
    </section>
  );
}

export function IngressSection({ form }: GuidedSectionProps) {
  const { value, kind, set, setNumber } = form;
  if (kind !== "Ingress") return null;
  const backendPath = [
    "spec",
    "rules",
    0,
    "http",
    "paths",
    0,
    "backend",
    "service",
  ] as const;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">
        Ingress route
      </h3>
      <Field label="Host">
        <Input
          value={stringValue(value, ["spec", "rules", 0, "host"])}
          onChange={(event) =>
            set(["spec", "rules", 0, "host"], event.target.value)
          }
        />
      </Field>
      <Field label="Path">
        <Input
          value={stringValue(value, [
            "spec",
            "rules",
            0,
            "http",
            "paths",
            0,
            "path",
          ])}
          onChange={(event) =>
            set(
              ["spec", "rules", 0, "http", "paths", 0, "path"],
              event.target.value,
            )
          }
        />
      </Field>
      <Field label="Backend service">
        <Input
          value={stringValue(value, [...backendPath, "name"])}
          onChange={(event) =>
            set([...backendPath, "name"], event.target.value)
          }
        />
      </Field>
      <Field label="Backend port">
        <Input
          type="number"
          min={1}
          max={65535}
          value={stringValue(value, [...backendPath, "port", "number"])}
          onChange={(event) =>
            setNumber([...backendPath, "port", "number"], event.target.value)
          }
        />
      </Field>
    </section>
  );
}
