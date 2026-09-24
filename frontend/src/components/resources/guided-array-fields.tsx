import type { ReactNode } from "react";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Field, type GuidedFormState } from "./guided-resource-fields";
import {
  manifestValue,
  stringValue,
  type ManifestPath,
} from "./guided-resource-model";

export function manifestArray(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value : [];
}

export function ArrayRows({
  form,
  path,
  label,
  create,
  children,
  canRemove,
}: {
  form: GuidedFormState;
  path: ManifestPath;
  label: string;
  create: () => Record<string, unknown>;
  children: (path: ManifestPath, index: number) => ReactNode;
  canRemove?: (item: Record<string, unknown>, index: number) => boolean;
}) {
  const items = manifestArray(manifestValue(form.value, path));
  return (
    <fieldset className="space-y-3 rounded-md border border-border p-3 md:col-span-2">
      <legend className="px-1 text-sm font-medium">{label}</legend>
      {items.map((item, index) => (
        <fieldset
          key={index}
          className="grid gap-3 rounded-md border border-border p-3 md:grid-cols-2"
        >
          <legend className="px-1 text-xs">
            {label} {index + 1}
          </legend>
          {children([...path, index], index)}
          <ActionButton
            type="button"
            size="sm"
            onClick={() =>
              form.set(
                path,
                items.filter((_, i) => i !== index),
              )
            }
            disabled={canRemove ? !canRemove(item, index) : false}
            disabledReason="Remove references to this item first."
            aria-label={`Remove ${label} ${index + 1}`}
          >
            Remove
          </ActionButton>
        </fieldset>
      ))}
      <ActionButton
        type="button"
        size="sm"
        onClick={() => form.set(path, [...items, create()])}
      >
        Add {label}
      </ActionButton>
    </fieldset>
  );
}

export function PathField({
  form,
  path,
  label,
  type = "text",
  options,
}: {
  form: GuidedFormState;
  path: ManifestPath;
  label: string;
  type?: "text" | "number" | "int-or-string" | "csv";
  options?: string[];
}) {
  const raw = manifestValue(form.value, path);
  const value =
    type === "csv" && Array.isArray(raw)
      ? raw.join(",")
      : stringValue(form.value, path);
  const change = (next: string) => {
    if (type === "number") form.setNumber(path, next);
    else if (type === "int-or-string")
      form.set(path, /^\d+$/.test(next) ? Number(next) : next);
    else if (type === "csv")
      form.set(
        path,
        next === "" && path.at(-1) !== "apiGroups"
          ? []
          : next.split(",").map((item) => item.trim()),
      );
    else form.set(path, next);
  };
  return (
    <Field
      label={label}
      error={form.errors[path.join(".")]}
      description={type === "csv" ? "Comma-separated values." : undefined}
    >
      {options ? (
        <Select value={value} onChange={(event) => change(event.target.value)}>
          <option value="">Default</option>
          {options.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </Select>
      ) : (
        <Input
          type={type === "number" ? "number" : "text"}
          value={value}
          onChange={(event) => change(event.target.value)}
        />
      )}
    </Field>
  );
}

export function PortFields({
  form,
  path,
  service = false,
}: {
  form: GuidedFormState;
  path: ManifestPath;
  service?: boolean;
}) {
  return (
    <ArrayRows
      form={form}
      path={path}
      label={service ? "Service ports" : "Container ports"}
      create={() =>
        service ? { port: 80, targetPort: 80 } : { containerPort: 8080 }
      }
    >
      {(item) => (
        <>
          <PathField form={form} path={[...item, "name"]} label="Port name" />
          <PathField
            form={form}
            path={[...item, service ? "port" : "containerPort"]}
            type="number"
            label={service ? "Port" : "Container port"}
          />
          <PathField
            form={form}
            path={[...item, "protocol"]}
            label="Protocol"
            options={["TCP", "UDP", "SCTP"]}
          />
          {service && (
            <PathField
              form={form}
              path={[...item, "targetPort"]}
              type="int-or-string"
              label="Target port"
            />
          )}
        </>
      )}
    </ArrayRows>
  );
}
