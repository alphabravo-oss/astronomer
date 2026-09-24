import { validateAffinity } from "./guided-affinity-validation";
import { validateIngress } from "./guided-ingress-validation";

export type KubernetesManifest = Record<string, unknown>;

export type SchemaMap = Record<string, unknown>;
export type ManifestPath = Array<string | number>;

const DNS_SUBDOMAIN = /^[a-z0-9](?:[-a-z0-9.]*[a-z0-9])?$/;
const DNS_LABEL = /^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$/;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function manifestValue(root: unknown, path: ManifestPath): unknown {
  let current = root;
  for (const segment of path) {
    if (typeof segment === "number") {
      if (!Array.isArray(current)) return undefined;
      current = current[segment];
    } else {
      current = asRecord(current)[segment];
    }
  }
  return current;
}

/** Immutable path update for plain Kubernetes JSON objects. */
export function updateManifest(
  source: KubernetesManifest,
  path: ManifestPath,
  value: unknown,
): KubernetesManifest {
  const root = structuredClone(source);
  let current: unknown = root;
  for (let index = 0; index < path.length - 1; index += 1) {
    const segment = path[index];
    const next = path[index + 1];
    if (typeof segment === "number") {
      if (!Array.isArray(current)) return root;
      if (current[segment] === undefined) {
        current[segment] = typeof next === "number" ? [] : {};
      }
      current = current[segment];
      continue;
    }
    const record = asRecord(current);
    if (record[segment] === undefined) {
      record[segment] = typeof next === "number" ? [] : {};
    }
    current = record[segment];
  }
  const leaf = path[path.length - 1];
  if (typeof leaf === "number") {
    if (Array.isArray(current)) current[leaf] = value;
  } else {
    const record = asRecord(current);
    if (value === undefined || value === "") delete record[leaf];
    else record[leaf] = value;
  }
  return root;
}

function resolveSchema(
  schema: SchemaMap,
  definitions: Record<string, SchemaMap>,
): SchemaMap {
  const ref = typeof schema.$ref === "string" ? schema.$ref : "";
  const prefix = "#/components/schemas/";
  if (!ref.startsWith(prefix)) return schema;
  return definitions[ref.slice(prefix.length)] ?? schema;
}

export function descriptionForPath(
  root: SchemaMap,
  definitions: Record<string, SchemaMap>,
  path: ManifestPath,
): string | undefined {
  let current = resolveSchema(root, definitions);
  for (const segment of path) {
    current = resolveSchema(current, definitions);
    if (typeof segment === "number") {
      current = resolveSchema(asRecord(current.items), definitions);
    } else {
      current = resolveSchema(
        asRecord(asRecord(current.properties)[segment]),
        definitions,
      );
    }
  }
  return typeof current.description === "string"
    ? current.description
    : undefined;
}

export function podSpecPath(kind: string): ManifestPath | null {
  switch (kind) {
    case "Deployment":
    case "StatefulSet":
    case "DaemonSet":
    case "Job":
      return ["spec", "template", "spec"];
    case "CronJob":
      return ["spec", "jobTemplate", "spec", "template", "spec"];
    default:
      return null;
  }
}

export function containerPath(kind: string): ManifestPath | null {
  const pod = podSpecPath(kind);
  return pod ? [...pod, "containers", 0] : null;
}

export function stringValue(
  manifest: KubernetesManifest,
  path: ManifestPath,
): string {
  const value = manifestValue(manifest, path);
  return value === undefined || value === null ? "" : String(value);
}

export function booleanValue(
  manifest: KubernetesManifest,
  path: ManifestPath,
): boolean {
  return manifestValue(manifest, path) === true;
}

export function keyValueText(value: unknown): string {
  return Object.entries(asRecord(value))
    .map(([key, item]) => `${key}=${String(item)}`)
    .join("\n");
}

export function parseKeyValueText(value: string): Record<string, string> {
  const result: Record<string, string> = {};
  for (const line of value.split("\n")) {
    const separator = line.indexOf("=");
    if (separator <= 0) continue;
    const key = line.slice(0, separator).trim();
    if (key) result[key] = line.slice(separator + 1).trim();
  }
  return result;
}

export function envText(value: unknown): string {
  if (!Array.isArray(value)) return "";
  return value
    .map((item) => asRecord(item))
    .filter((item) => typeof item.name === "string" && "value" in item)
    .map((item) => `${item.name}=${String(item.value ?? "")}`)
    .join("\n");
}

export function parseEnvText(
  value: string,
  existing: unknown = [],
): Array<Record<string, unknown>> {
  const literals = new Map(Object.entries(parseKeyValueText(value)));
  const result: Array<Record<string, unknown>> = [];
  for (const item of Array.isArray(existing) ? existing : []) {
    const entry = asRecord(item);
    const name = String(entry.name ?? "");
    if ("valueFrom" in entry) result.push(entry);
    else if (literals.has(name))
      result.push({ ...entry, value: literals.get(name) });
    literals.delete(name);
  }
  return [
    ...result,
    ...Array.from(literals, ([name, item]) => ({ name, value: item })),
  ];
}

/** The three probe kinds a container spec accepts, keyed by their manifest field. */
export const PROBE_KEYS = [
  "readinessProbe",
  "livenessProbe",
  "startupProbe",
] as const;

/**
 * A configured httpGet/tcpSocket probe with no port (or a non-1-65535 one)
 * is rejected by the apiserver; an exec probe with no command args is
 * meaningless. Returns an error message, or null when the probe is absent
 * or valid. Absent probes are never flagged — probes are optional.
 */
function probeError(
  manifest: KubernetesManifest,
  probePath: ManifestPath,
): string | null {
  const probe = asRecord(manifestValue(manifest, probePath));
  if (Object.keys(probe).length === 0) return null;
  if ("httpGet" in probe || "tcpSocket" in probe) {
    const target = asRecord(
      "httpGet" in probe ? probe.httpGet : probe.tcpSocket,
    );
    const port = target.port;
    const valid =
      typeof port === "number"
        ? Number.isInteger(port) && port >= 1 && port <= 65535
        : typeof port === "string" &&
          port.length <= 15 &&
          /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(port) &&
          /[a-z]/.test(port);
    if (!valid) {
      return "Port must be an integer from 1 to 65535 or a valid named port.";
    }
    return null;
  }
  if ("exec" in probe) {
    const command = asRecord(probe.exec).command;
    if (!Array.isArray(command) || command.length === 0) {
      return "Exec probes need at least one command argument.";
    }
  }
  return null;
}

export function validateGuidedContainer(
  manifest: KubernetesManifest,
  path: ManifestPath,
): Record<string, string> {
  const errors: Record<string, string> = {};
  if (!stringValue(manifest, [...path, "image"]))
    errors.image = "A container image is required.";
  const ports = manifestValue(manifest, [...path, "ports"]);
  for (const entry of Array.isArray(ports) ? ports : []) {
    const port = asRecord(entry).containerPort;
    if (
      typeof port !== "number" ||
      !Number.isInteger(port) ||
      port < 1 ||
      port > 65535
    )
      errors.containerPort = "Port must be an integer from 1 to 65535.";
  }
  for (const key of PROBE_KEYS) {
    const error = probeError(manifest, [...path, key]);
    if (error) errors[key] = error;
  }
  return errors;
}

export function validateGuidedResource(
  manifest: KubernetesManifest,
): Record<string, string> {
  const errors: Record<string, string> = {};
  const kind = stringValue(manifest, ["kind"]);
  const name = stringValue(manifest, ["metadata", "name"]);
  const namespace = stringValue(manifest, ["metadata", "namespace"]);
  if (!name) errors.name = "Name is required.";
  else if (name.length > 253 || !DNS_SUBDOMAIN.test(name))
    errors.name = "Use a lowercase DNS name (letters, digits, '-' or '.').";
  if (kind !== "Namespace" && namespace && !DNS_LABEL.test(namespace))
    errors.namespace = "Namespace must be a lowercase DNS label.";

  const pod = podSpecPath(kind);
  if (pod) {
    Object.assign(
      errors,
      validateAffinity(manifestValue(manifest, [...pod, "affinity"]), [
        ...pod,
        "affinity",
      ]),
    );
    for (const group of ["containers", "initContainers"]) {
      const raw = manifestValue(manifest, [...pod, group]);
      const items = Array.isArray(raw)
        ? raw
        : group === "containers"
          ? [{}]
          : [];
      if (group === "containers" && items.length === 0)
        errors.image = "At least one container is required.";
      items.forEach((_, index) => {
        for (const [key, message] of Object.entries(
          validateGuidedContainer(manifest, [...pod, group, index]),
        )) {
          errors[
            group === "containers" && index === 0
              ? key
              : `${group}.${index}.${key}`
          ] = message;
        }
      });
    }
  }
  if (kind === "Service") {
    const ports = manifestValue(manifest, ["spec", "ports"]);
    if (Array.isArray(ports) && ports.length > 0)
      ports.forEach((_, index) => {
        const port = Number(
          stringValue(manifest, ["spec", "ports", index, "port"]),
        );
        if (!Number.isInteger(port) || port < 1 || port > 65535)
          errors[index === 0 ? "servicePort" : `ports.${index}.port`] =
            "Service port must be an integer from 1 to 65535.";
      });
    else if (stringValue(manifest, ["spec", "type"]) !== "ExternalName")
      errors.servicePort = "At least one Service port is required.";
  }
  if (kind === "Ingress")
    Object.assign(errors, validateIngress(manifestValue(manifest, ["spec"])));
  if (kind === "CronJob") {
    const schedule = stringValue(manifest, ["spec", "schedule"]);
    if (schedule.trim().split(/\s+/).length !== 5)
      errors.schedule = "Use a five-field cron schedule.";
  }
  if (kind === "HorizontalPodAutoscaler") {
    const min = Number(stringValue(manifest, ["spec", "minReplicas"]));
    const max = Number(stringValue(manifest, ["spec", "maxReplicas"]));
    const target = stringValue(manifest, ["spec", "scaleTargetRef", "name"]);
    if (!target) errors.scaleTarget = "A target workload name is required.";
    if (!Number.isInteger(min) || min < 1)
      errors.minReplicas = "Minimum replicas must be at least 1.";
    if (!Number.isInteger(max) || max < min)
      errors.maxReplicas = "Maximum replicas must be at least the minimum.";
  }
  if (
    kind === "PersistentVolumeClaim" &&
    !stringValue(manifest, ["spec", "resources", "requests", "storage"])
  ) {
    errors.storage = "Requested storage is required.";
  }
  return errors;
}
