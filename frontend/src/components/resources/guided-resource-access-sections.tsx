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

type SecretType =
  | "Opaque"
  | "kubernetes.io/tls"
  | "kubernetes.io/basic-auth"
  | "kubernetes.io/ssh-auth";

export function SecretSection({ form }: GuidedSectionProps) {
  const { value, kind, onChange, set } = form;
  if (kind !== "Secret") return null;
  const secretType = (stringValue(value, ["type"]) || "Opaque") as SecretType;

  const setSecretType = (nextType: SecretType) => {
    let next = updateManifest(
      value,
      ["type"],
      nextType === "Opaque" ? undefined : nextType,
    );
    // Switching type retires whatever the previous type's fields wrote —
    // an Opaque freeform textarea and a typed secret's fixed keys don't mix.
    next = updateManifest(next, ["stringData"], {});
    next = updateManifest(next, ["data"], undefined);
    onChange(next);
  };

  return (
    <section className="rounded-lg border border-border bg-card p-4 space-y-3">
      <h3 className="font-medium text-foreground">Secret</h3>
      <Field label="Type">
        <Select
          value={secretType}
          onChange={(event) => setSecretType(event.target.value as SecretType)}
        >
          <option value="Opaque">Opaque</option>
          <option value="kubernetes.io/tls">TLS (kubernetes.io/tls)</option>
          <option value="kubernetes.io/basic-auth">
            Basic auth (kubernetes.io/basic-auth)
          </option>
          <option value="kubernetes.io/ssh-auth">
            SSH auth (kubernetes.io/ssh-auth)
          </option>
        </Select>
      </Field>

      {secretType === "Opaque" && (
        <Field
          label="Values"
          description="One key=value pair per line. Values are sent only to the Kubernetes API and are never written to browser storage."
        >
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
        </Field>
      )}

      {secretType === "kubernetes.io/tls" && (
        <>
          <Field label="Certificate (tls.crt)">
            <Textarea
              value={stringValue(value, ["stringData", "tls.crt"])}
              onChange={(event) =>
                set(["stringData", "tls.crt"], event.target.value)
              }
              rows={4}
              autoComplete="off"
            />
          </Field>
          <Field label="Private key (tls.key)">
            <Textarea
              value={stringValue(value, ["stringData", "tls.key"])}
              onChange={(event) =>
                set(["stringData", "tls.key"], event.target.value)
              }
              rows={4}
              autoComplete="off"
            />
          </Field>
        </>
      )}

      {secretType === "kubernetes.io/basic-auth" && (
        <>
          <Field label="Username">
            <Input
              value={stringValue(value, ["stringData", "username"])}
              onChange={(event) =>
                set(["stringData", "username"], event.target.value)
              }
              autoComplete="off"
            />
          </Field>
          <Field label="Password">
            <Input
              type="password"
              value={stringValue(value, ["stringData", "password"])}
              onChange={(event) =>
                set(["stringData", "password"], event.target.value)
              }
              autoComplete="off"
            />
          </Field>
        </>
      )}

      {secretType === "kubernetes.io/ssh-auth" && (
        <Field label="Private key (ssh-privatekey)">
          <Textarea
            value={stringValue(value, ["stringData", "ssh-privatekey"])}
            onChange={(event) =>
              set(["stringData", "ssh-privatekey"], event.target.value)
            }
            rows={5}
            autoComplete="off"
          />
        </Field>
      )}
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
