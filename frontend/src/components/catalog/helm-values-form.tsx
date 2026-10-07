import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import {
  appendArrayItem,
  defaultValueForSchema,
  getValueAtPath,
  removeArrayItem,
  setValueAtPath,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";
import { Plus, Trash2 } from "lucide-react";

interface HelmValuesFormProps {
  schema: HelmValuesSchemaNode;
  value: HelmValuesObject;
  onChange: (next: HelmValuesObject) => void;
  path?: string[];
}

function labelFor(schema: HelmValuesSchemaNode, key: string): string {
  return schema.title || key;
}

function helpText(schema: HelmValuesSchemaNode) {
  return schema.description ? (
    <p className="text-xs text-muted-foreground mt-1 leading-relaxed">
      {schema.description}
    </p>
  ) : null;
}

function schemaType(schema: HelmValuesSchemaNode): string {
  if (Array.isArray(schema.type))
    return schema.type.find((t) => t !== "null") || schema.type[0] || "string";
  if (schema.type) return schema.type;
  if (schema.properties) return "object";
  if (schema.items) return "array";
  return "string";
}

export function HelmValuesForm({
  schema,
  value,
  onChange,
  path = [],
}: HelmValuesFormProps) {
  const type = schemaType(schema);

  if (type === "object") {
    const objectValue =
      (getValueAtPath(value, path) as Record<string, unknown> | undefined) ||
      {};
    return (
      <div
        className={cn(
          "space-y-4",
          path.length > 0 &&
            "rounded-lg border border-border/60 p-4 bg-muted/20",
        )}
      >
        {path.length > 0 && (
          <div>
            <h4 className="text-sm font-medium text-foreground">
              {schema.title || path[path.length - 1]}
            </h4>
            {helpText(schema)}
          </div>
        )}
        {Object.entries(schema.properties || {}).map(([key, childSchema]) => {
          const childPath = [...path, key];
          const condition = childSchema["x-astronomer-show-when"];
          if (
            condition &&
            String(getValueAtPath(value, condition.path.split("."))) !==
              condition.equals
          )
            return null;
          const childType = schemaType(childSchema);
          const required = schema.required?.includes(key) ?? false;
          const rawValue =
            objectValue[key] ?? defaultValueForSchema(childSchema);

          if (childType === "object" || childType === "array") {
            return (
              <div key={childPath.join(".")} className="space-y-2">
                {childType === "array" ? (
                  <ArrayField
                    schema={childSchema}
                    path={childPath}
                    rootValue={value}
                    onChange={onChange}
                  />
                ) : (
                  <HelmValuesForm
                    schema={childSchema}
                    value={value}
                    onChange={onChange}
                    path={childPath}
                  />
                )}
              </div>
            );
          }

          return (
            <ScalarField
              key={childPath.join(".")}
              id={`helm-value-${childPath.join("-")}`}
              schema={childSchema}
              label={labelFor(childSchema, key)}
              value={rawValue}
              required={required}
              onChange={(nextScalar) =>
                onChange(setValueAtPath(value, childPath, nextScalar))
              }
            />
          );
        })}
      </div>
    );
  }

  return null;
}

function ScalarField({
  id,
  schema,
  label,
  value,
  required = false,
  onChange,
}: {
  id: string;
  schema: HelmValuesSchemaNode;
  label: string;
  value: unknown;
  required?: boolean;
  onChange: (next: unknown) => void;
}) {
  const type = schemaType(schema);
  const multiline =
    schema.format === "multiline" || String(value ?? "").includes("\n");

  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor={id} className="text-sm font-medium text-foreground">
          {label}
          {required && <span aria-hidden="true"> *</span>}
        </label>
        {schema["x-astronomer-group"] && (
          <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
            {schema["x-astronomer-group"]}
          </span>
        )}
      </div>
      {type === "boolean" ? (
        <div className="inline-flex items-center gap-2 text-sm text-foreground">
          <input
            id={id}
            type="checkbox"
            required={required}
            checked={Boolean(value)}
            onChange={(e) => onChange(e.target.checked)}
            className="h-4 w-4 rounded-sm border-border"
          />
          <span aria-hidden="true">Enabled</span>
        </div>
      ) : schema.enum && schema.enum.length > 0 ? (
        <Select
          id={id}
          value={String(value ?? "")}
          required={required}
          onChange={(e) => {
            const selected = schema.enum?.find(
              (item) => String(item) === e.target.value,
            );
            onChange(selected ?? e.target.value);
          }}
        >
          {schema.enum.map((item) => (
            <option key={String(item)} value={String(item)}>
              {String(item)}
            </option>
          ))}
        </Select>
      ) : type === "integer" || type === "number" ? (
        <Input
          id={id}
          type="number"
          required={required}
          min={schema.minimum}
          max={schema.maximum}
          step={schema.multipleOf}
          value={typeof value === "number" ? value : Number(value ?? 0)}
          onChange={(e) =>
            onChange(
              type === "integer"
                ? parseInt(e.target.value || "0", 10)
                : parseFloat(e.target.value || "0"),
            )
          }
        />
      ) : multiline ? (
        <Textarea
          id={id}
          value={String(value ?? "")}
          required={required}
          onChange={(e) => onChange(e.target.value)}
          rows={6}
          className="min-h-0 resize-y"
        />
      ) : (
        <Input
          id={id}
          type={schema.format === "password" ? "password" : "text"}
          required={required}
          value={String(value ?? "")}
          minLength={schema.minLength}
          maxLength={schema.maxLength}
          pattern={schema.pattern}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {helpText(schema)}
    </div>
  );
}

function ArrayField({
  schema,
  path,
  rootValue,
  onChange,
}: {
  schema: HelmValuesSchemaNode;
  path: string[];
  rootValue: HelmValuesObject;
  onChange: (next: HelmValuesObject) => void;
}) {
  const itemsSchema = schema.items || { type: "string" };
  const itemType = schemaType(itemsSchema);
  const items = (getValueAtPath(rootValue, path) as unknown[]) || [];

  return (
    <div className="space-y-2 rounded-lg border border-border/60 p-4 bg-muted/20">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h4 className="text-sm font-medium text-foreground">
            {schema.title || path[path.length - 1]}
          </h4>
          {helpText(schema)}
        </div>
        <ActionButton
          size="sm"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => onChange(appendArrayItem(rootValue, path, schema))}
        >
          Add item
        </ActionButton>
      </div>

      {items.length === 0 ? (
        <p className="text-xs text-muted-foreground">No items yet.</p>
      ) : (
        <div className="space-y-3">
          {items.map((item, index) => {
            const itemPath = [...path, String(index)];
            return (
              <div
                key={itemPath.join(".")}
                className="rounded-md border border-border/50 bg-background/70 p-3 space-y-2"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    Item {index + 1}
                  </span>
                  <ActionButton
                    size="icon"
                    intent="ghost"
                    icon={<Trash2 className="h-3.5 w-3.5" />}
                    onClick={() =>
                      onChange(removeArrayItem(rootValue, path, index))
                    }
                    className="hover:text-status-error hover:bg-status-error/10"
                    aria-label={`Remove item ${index + 1}`}
                  />
                </div>
                {itemType === "object" ? (
                  <HelmValuesForm
                    schema={itemsSchema}
                    value={rootValue}
                    onChange={onChange}
                    path={itemPath}
                  />
                ) : (
                  <ScalarField
                    id={`helm-value-${itemPath.join("-")}`}
                    schema={itemsSchema}
                    label={itemsSchema.title || `Item ${index + 1}`}
                    value={item}
                    onChange={(nextScalar) =>
                      onChange(setValueAtPath(rootValue, itemPath, nextScalar))
                    }
                  />
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
