import { PathField } from "./guided-array-fields";
import { Field, type GuidedFormState } from "./guided-resource-fields";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  manifestValue,
  stringValue,
  type ManifestPath,
} from "./guided-resource-model";
import { CheckboxField } from "./guided-resource-fields";

export function IngressDefaultBackend({ form }: { form: GuidedFormState }) {
  const path = ["spec", "defaultBackend"];
  const enabled = manifestValue(form.value, path) != null;
  return (
    <fieldset className="space-y-3 rounded-md border border-border p-3">
      <legend>Default backend</legend>
      <CheckboxField
        label="Configure a default backend"
        checked={enabled}
        onChange={(checked) =>
          form.set(
            path,
            checked
              ? { service: { name: "", port: { number: 80 } } }
              : undefined,
          )
        }
      />
      {enabled && <IngressBackendFields form={form} path={path} />}
    </fieldset>
  );
}

export function IngressBackendFields({
  form,
  path,
}: {
  form: GuidedFormState;
  path: ManifestPath;
}) {
  const resource = manifestValue(form.value, [...path, "resource"]) != null;
  const service = manifestValue(form.value, [...path, "service"]) != null;
  return (
    <>
      <Field
        label="Backend type"
        description="Changing type replaces the previous backend reference."
        error={form.errors[path.join(".")]}
      >
        <Select
          value={
            resource && service ? "invalid" : resource ? "resource" : "service"
          }
          onChange={(event) => {
            const existing = manifestValue(form.value, path);
            const next =
              existing && typeof existing === "object"
                ? ({ ...existing } as Record<string, unknown>)
                : {};
            delete next.service;
            delete next.resource;
            const kind = event.target.value;
            next[kind] =
              manifestValue(form.value, [...path, kind]) ??
              (kind === "resource"
                ? { kind: "", name: "" }
                : { name: "", port: { number: 80 } });
            form.set(path, next);
          }}
        >
          {resource && service && (
            <option value="invalid" disabled>
              Invalid: both backends present
            </option>
          )}
          <option value="service">Service</option>
          <option value="resource">Resource reference</option>
        </Select>
      </Field>
      {resource ? (
        <>
          <PathField
            form={form}
            path={[...path, "resource", "apiGroup"]}
            label="Backend API group (optional)"
          />
          <PathField
            form={form}
            path={[...path, "resource", "kind"]}
            label="Backend resource kind"
          />
          <PathField
            form={form}
            path={[...path, "resource", "name"]}
            label="Backend resource name"
          />
          <p className="text-xs text-muted-foreground">
            The resource must be in the Ingress namespace and supported by its
            controller.
          </p>
        </>
      ) : (
        <>
          <PathField
            form={form}
            path={[...path, "service", "name"]}
            label="Backend service"
          />
          <Field
            label="Backend port"
            error={form.errors[[...path, "service", "port"].join(".")]}
          >
            <Input
              value={
                stringValue(form.value, [
                  ...path,
                  "service",
                  "port",
                  "number",
                ]) ||
                stringValue(form.value, [...path, "service", "port", "name"])
              }
              onChange={(event) => {
                const value = event.target.value;
                form.set(
                  [...path, "service", "port"],
                  /^\d+$/.test(value)
                    ? { number: Number(value) }
                    : { name: value },
                );
              }}
            />
          </Field>
        </>
      )}
    </>
  );
}
