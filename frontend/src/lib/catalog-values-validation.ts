import type { HelmValuesSchemaNode } from "./helm-values-schema";
export interface CatalogValueError {
  path: string;
  message: string;
}
/** Immediate field feedback. The server remains authoritative for full JSON Schema validation. */
export function catalogValueErrors(
  schema: HelmValuesSchemaNode | null,
  value: unknown,
  path = "values",
): CatalogValueError[] {
  if (!schema || value === undefined) return [];
  const errors: CatalogValueError[] = [];
  const fail = (message: string) => errors.push({ path, message });
  const types = Array.isArray(schema.type)
    ? schema.type
    : schema.type
      ? [schema.type]
      : [];
  const actual =
    value === null ? "null" : Array.isArray(value) ? "array" : typeof value;
  if (
    types.length &&
    !types.includes(actual) &&
    !(
      actual === "number" &&
      types.includes("integer") &&
      Number.isInteger(value)
    )
  ) {
    fail(`Expected ${types.join(" or ")}.`);
    return errors;
  }
  if (
    schema.enum &&
    !schema.enum.some((item) => JSON.stringify(item) === JSON.stringify(value))
  )
    fail("Choose an available option.");
  if (typeof value === "number") {
    if (schema.minimum !== undefined && value < schema.minimum)
      fail(`Must be at least ${schema.minimum}.`);
    if (schema.maximum !== undefined && value > schema.maximum)
      fail(`Must be at most ${schema.maximum}.`);
  }
  if (typeof value === "string") {
    if (schema.minLength !== undefined && value.length < schema.minLength)
      fail(`Must contain at least ${schema.minLength} characters.`);
    if (schema.maxLength !== undefined && value.length > schema.maxLength)
      fail(`Must contain at most ${schema.maxLength} characters.`);
    if (schema.pattern) {
      try {
        if (!new RegExp(schema.pattern).test(value))
          fail("Does not match the required format.");
      } catch {
        fail("This field's validation metadata is invalid.");
      }
    }
  }
  if (Array.isArray(value))
    value.forEach((item, index) =>
      errors.push(
        ...catalogValueErrors(schema.items ?? null, item, `${path}[${index}]`),
      ),
    );
  else if (value !== null && typeof value === "object") {
    const object = value as Record<string, unknown>;
    for (const key of schema.required ?? [])
      if (!(key in object))
        errors.push({ path: `${path}.${key}`, message: "Required." });
    for (const [key, child] of Object.entries(schema.properties ?? {}))
      errors.push(...catalogValueErrors(child, object[key], `${path}.${key}`));
  }
  return errors;
}
