import {
  Field,
  type GuidedSectionProps,
} from "@/components/resources/guided-resource-fields";
import {
  keyValueText,
  manifestValue,
  parseKeyValueText,
  stringValue,
  updateManifest,
} from "@/components/resources/guided-resource-model";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

function commaSeparated(value: unknown): string {
  return Array.isArray(value) ? (value as string[]).join(",") : "";
}

function parseCommaSeparated(value: string): string[] {
  return value.split(",").map((item) => item.trim());
}

export function SecretSection({ form }: GuidedSectionProps) {
  const { value, kind, onChange } = form;
  if (kind !== "Secret") return null;
  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <h3 className="font-medium text-foreground">Secret values</h3>
      <p className="mb-3 mt-1 text-xs text-muted-foreground">
        One key=value pair per line. Values are sent only to the Kubernetes API
        and are never written to browser storage.
      </p>
      <Textarea
        value={keyValueText(manifestValue(value, ["stringData"]))}
        onChange={(event) => {
          let next = updateManifest(
            value,
            ["stringData"],
            parseKeyValueText(event.target.value),
          );
          next = updateManifest(next, ["data"], undefined);
          onChange(next);
        }}
        rows={5}
        autoComplete="off"
      />
    </section>
  );
}

export function RoleSection({ form }: GuidedSectionProps) {
  const { value, kind, set } = form;
  if (kind !== "Role") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">RBAC rule</h3>
      <Field
        label="API groups"
        description="Comma-separated; use an empty value for core APIs."
      >
        <Input
          value={commaSeparated(
            manifestValue(value, ["rules", 0, "apiGroups"]),
          )}
          onChange={(event) =>
            set(
              ["rules", 0, "apiGroups"],
              parseCommaSeparated(event.target.value),
            )
          }
        />
      </Field>
      <Field
        label="Resources"
        description="Comma-separated Kubernetes resources."
      >
        <Input
          value={commaSeparated(
            manifestValue(value, ["rules", 0, "resources"]),
          )}
          onChange={(event) =>
            set(
              ["rules", 0, "resources"],
              parseCommaSeparated(event.target.value),
            )
          }
        />
      </Field>
      <Field
        label="Verbs"
        description="Comma-separated verbs; avoid wildcards."
      >
        <Input
          value={commaSeparated(manifestValue(value, ["rules", 0, "verbs"]))}
          onChange={(event) =>
            set(["rules", 0, "verbs"], parseCommaSeparated(event.target.value))
          }
        />
      </Field>
    </section>
  );
}

export function RoleBindingSection({ form }: GuidedSectionProps) {
  const { value, kind, set } = form;
  if (kind !== "RoleBinding") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">
        Role binding
      </h3>
      <Field label="Subject kind">
        <Select
          value={stringValue(value, ["subjects", 0, "kind"])}
          onChange={(event) => set(["subjects", 0, "kind"], event.target.value)}
        >
          <option value="User">User</option>
          <option value="Group">Group</option>
          <option value="ServiceAccount">ServiceAccount</option>
        </Select>
      </Field>
      <Field label="Subject name">
        <Input
          value={stringValue(value, ["subjects", 0, "name"])}
          onChange={(event) => set(["subjects", 0, "name"], event.target.value)}
        />
      </Field>
      <Field label="Role name">
        <Input
          value={stringValue(value, ["roleRef", "name"])}
          onChange={(event) => set(["roleRef", "name"], event.target.value)}
        />
      </Field>
    </section>
  );
}

export function GatewaySection({ form }: GuidedSectionProps) {
  const { value, kind, set, setNumber } = form;
  if (kind !== "Gateway") return null;
  return (
    <section className="grid gap-4 rounded-lg border border-border bg-card p-4 md:grid-cols-2">
      <h3 className="font-medium text-foreground md:col-span-2">
        Gateway listener
      </h3>
      <Field label="Gateway class">
        <Input
          value={stringValue(value, ["spec", "gatewayClassName"])}
          onChange={(event) =>
            set(["spec", "gatewayClassName"], event.target.value)
          }
        />
      </Field>
      <Field label="Listener name">
        <Input
          value={stringValue(value, ["spec", "listeners", 0, "name"])}
          onChange={(event) =>
            set(["spec", "listeners", 0, "name"], event.target.value)
          }
        />
      </Field>
      <Field label="Protocol">
        <Select
          value={stringValue(value, ["spec", "listeners", 0, "protocol"])}
          onChange={(event) =>
            set(["spec", "listeners", 0, "protocol"], event.target.value)
          }
        >
          <option value="HTTP">HTTP</option>
          <option value="HTTPS">HTTPS</option>
          <option value="TLS">TLS</option>
          <option value="TCP">TCP</option>
          <option value="UDP">UDP</option>
        </Select>
      </Field>
      <Field label="Port">
        <Input
          type="number"
          min={1}
          max={65535}
          value={stringValue(value, ["spec", "listeners", 0, "port"])}
          onChange={(event) =>
            setNumber(["spec", "listeners", 0, "port"], event.target.value)
          }
        />
      </Field>
    </section>
  );
}
