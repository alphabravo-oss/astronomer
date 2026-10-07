import type { ToolFormField } from "@/types";
import * as yaml from "js-yaml";
export function parseOverride(raw: string): Record<string, unknown> | null {
  if (!raw.trim()) return {};
  try {
    const parsed = yaml.load(raw);
    if (!parsed) return {};
    return typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

export function valueAtPath(
  root: Record<string, unknown>,
  path: string,
): unknown {
  let value: unknown = root;
  for (const segment of path.split(".")) {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      return undefined;
    }
    value = (value as Record<string, unknown>)[segment];
  }
  return value;
}

export function setPath(
  root: Record<string, unknown>,
  path: string,
  value: unknown,
) {
  const parts = path.split(".");
  let node = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const k = parts[i];
    if (typeof node[k] !== "object" || node[k] === null) node[k] = {};
    node = node[k] as Record<string, unknown>;
  }
  node[parts[parts.length - 1]] = value;
}

export function withPath(
  root: Record<string, unknown>,
  path: string,
  value: unknown,
): Record<string, unknown> {
  const next = structuredClone(root);
  setPath(next, path, value);
  return next;
}

export function withoutPath(
  root: Record<string, unknown>,
  path: string,
): Record<string, unknown> {
  const next = structuredClone(root);
  const parts = path.split(".");
  const parents: Array<[Record<string, unknown>, string]> = [];
  let node = next;
  for (const part of parts.slice(0, -1)) {
    const child = node[part];
    if (!child || typeof child !== "object" || Array.isArray(child)) {
      return next;
    }
    parents.push([node, part]);
    node = child as Record<string, unknown>;
  }
  delete node[parts.at(-1)!];
  for (const [parent, key] of parents.reverse()) {
    const child = parent[key];
    if (
      child &&
      typeof child === "object" &&
      !Array.isArray(child) &&
      Object.keys(child).length === 0
    ) {
      delete parent[key];
    }
  }
  return next;
}

export function coerce(field: ToolFormField, raw: string): unknown {
  if (field.type === "number") {
    const n = Number(raw);
    return Number.isFinite(n) ? n : raw;
  }
  if (field.type === "boolean") return raw === "true";
  return raw;
}

export function groupFields(
  fields: ToolFormField[],
): Array<[string, ToolFormField[]]> {
  const order = [
    "General",
    "Base CRDs",
    "Control plane",
    "Scaling",
    "Storage",
    "Networking",
    "Pipeline",
    "Scanners",
    "Runtime",
    "Security",
    "Injection",
    "Telemetry",
    "Monitoring",
    "Scheduling",
    "Resources",
  ];
  const byGroup = new Map<string, ToolFormField[]>();
  for (const f of fields) {
    const g = f.group || "General";
    if (!byGroup.has(g)) byGroup.set(g, []);
    byGroup.get(g)!.push(f);
  }
  return Array.from(byGroup.entries()).sort(
    (a, b) => (order.indexOf(a[0]) + 1 || 99) - (order.indexOf(b[0]) + 1 || 99),
  );
}
