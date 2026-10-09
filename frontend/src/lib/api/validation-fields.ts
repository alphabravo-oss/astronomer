import { apiErrorCode, apiErrorStatus } from "./errors";

export interface ServerFieldError {
  name: string;
  message: string;
}

/** Map only owned validation fields to safe local labels; never display upstream values. */
export function apiValidationFields(
  error: unknown,
  fields: Readonly<Record<string, { name: string; label: string }>>,
): ServerFieldError[] {
  if (
    apiErrorStatus(error) !== 422 ||
    apiErrorCode(error) !== "validation_error"
  )
    return [];
  const data = (
    error as { response?: { data?: { error?: { fields?: unknown } } } }
  ).response?.data?.error?.fields;
  if (!Array.isArray(data)) return [];
  const result = new Map<string, ServerFieldError>();
  for (const item of data.slice(0, 32)) {
    if (!item || typeof item !== "object" || typeof item.field !== "string")
      continue;
    if (!Object.prototype.hasOwnProperty.call(fields, item.field)) continue;
    const field = fields[item.field];
    const message =
      item.rule === "invalid_regex"
        ? `${field.label} contain an invalid regular expression.`
        : `Check ${field.label.toLowerCase()}.`;
    if (!result.has(field.name))
      result.set(field.name, { name: field.name, message });
  }
  return [...result.values()];
}
