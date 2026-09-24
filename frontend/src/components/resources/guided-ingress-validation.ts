import type { ManifestPath } from "./guided-resource-model";

type Row = Record<string, unknown>;
const record = (value: unknown): Row =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Row)
    : {};
const rows = (value: unknown): Row[] =>
  Array.isArray(value) ? value.map(record) : [];

export function validateIngress(spec: unknown): Record<string, string> {
  const errors: Record<string, string> = {};
  const backend = (value: unknown, path: ManifestPath) => {
    const raw = record(value);
    const service = raw.service != null;
    const resource = raw.resource != null;
    if (service === resource) {
      errors[path.join(".")] =
        "Choose exactly one service or resource backend.";
      return;
    }
    const kind = service ? "service" : "resource";
    const item = record(raw[kind]);
    for (const field of service ? ["name"] : ["name", "kind"])
      if (typeof item[field] !== "string" || !String(item[field]).trim())
        errors[[...path, kind, field].join(".")] =
          `Backend ${field} is required.`;
    if (service) {
      const port = record(item.port);
      const number = port.number != null;
      const name = typeof port.name === "string" && port.name.length > 0;
      if (
        number === name ||
        (number &&
          (!Number.isInteger(port.number) ||
            Number(port.number) < 1 ||
            Number(port.number) > 65535))
      )
        errors[[...path, "service", "port"].join(".")] =
          "Use one named port or an integer port from 1 to 65535.";
    }
  };
  const root = record(spec);
  if (root.defaultBackend != null)
    backend(root.defaultBackend, ["spec", "defaultBackend"]);
  rows(root.rules).forEach((rule, index) =>
    rows(record(rule.http).paths).forEach((item, pathIndex) =>
      backend(item.backend, [
        "spec",
        "rules",
        index,
        "http",
        "paths",
        pathIndex,
        "backend",
      ]),
    ),
  );
  return errors;
}
