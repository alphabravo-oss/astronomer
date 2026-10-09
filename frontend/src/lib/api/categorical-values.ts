import {
  categoricalValues,
  type CategoryName,
  type CategoryValue,
} from "@/types/categories.generated";
import {
  categoricalDefinitions,
  categoricalResponses,
  type CategoricalNode,
} from "@/types/categorical-responses.generated";

export type CategoryResult<K extends CategoryName> =
  | { kind: "known"; category: K; value: CategoryValue<K> }
  | { kind: "unknown"; category: K };

export function classifyCategory<K extends CategoryName>(
  category: K,
  value: unknown,
): CategoryResult<K> {
  const allowed: readonly string[] = categoricalValues[category];
  return typeof value === "string" && allowed.includes(value)
    ? { kind: "known", category, value: value as CategoryValue<K> }
    : { kind: "unknown", category };
}

export class CategoricalResponseError extends Error {
  readonly code = "invalid_categorical_response";
  constructor(
    readonly kind: "unknown" | "malformed",
    readonly category?: CategoryName,
  ) {
    super("The server returned an unsupported categorical response.");
    this.name = "CategoricalResponseError";
  }
}

function checkNode(
  node: CategoricalNode,
  value: unknown,
  depth: number,
): unknown {
  if (depth > 128) throw new CategoricalResponseError("malformed");
  if (value === null && node.nullable) return value;
  if (node.category) {
    if (typeof value !== "string")
      throw new CategoricalResponseError("malformed", node.category);
    if (classifyCategory(node.category, value).kind === "unknown")
      return "unsupported";
  }
  if (node.ref) {
    const resolved = categoricalDefinitions[node.ref];
    if (!resolved) throw new CategoricalResponseError("malformed");
    value = checkNode(resolved, value, depth + 1);
  }
  for (const child of node.all ?? [])
    value = checkNode(child, value, depth + 1);
  if (node.items) {
    if (!Array.isArray(value)) throw new CategoricalResponseError("malformed");
    value = value.map((item) => checkNode(node.items!, item, depth + 1));
  }
  if (node.properties) {
    if (!value || typeof value !== "object" || Array.isArray(value))
      throw new CategoricalResponseError("malformed");
    const mapped: Record<string, unknown> = {
      ...(value as Record<string, unknown>),
    };
    for (const [field, binding] of Object.entries(node.properties)) {
      if (!Object.hasOwn(value, field)) {
        if (binding.required) throw new CategoricalResponseError("malformed");
        continue;
      }
      mapped[field] = checkNode(
        binding.node,
        (value as Record<string, unknown>)[field],
        depth + 1,
      );
    }
    value = mapped;
  }
  return value;
}

export function validateCategoricalResponse(
  operation: string,
  status: number,
  value: unknown,
): unknown {
  const responses = categoricalResponses[operation];
  if (!responses) return value;
  const node = responses[String(status)];
  if (!Object.hasOwn(responses, String(status)))
    throw new CategoricalResponseError("malformed");
  if (node === null) return value;
  return checkNode(node, value, 0);
}
