import {
  manifestValue,
  type ManifestPath,
} from "@/components/resources/guided-resource-model";
import {
  Field,
  type GuidedSectionProps,
} from "@/components/resources/guided-resource-fields";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

export type ProbeKind = "readiness" | "liveness" | "startup";
type ProbeType = "none" | "httpGet" | "tcpSocket" | "exec";

function probePortValue(value: string): string | number {
  return /^\d+$/.test(value) ? Number(value) : value;
}

const PROBE_LABEL: Record<ProbeKind, string> = {
  readiness: "Readiness probe",
  liveness: "Liveness probe",
  startup: "Startup probe",
};

const PROBE_NUMBER_FIELDS = [
  ["initialDelaySeconds", "Initial delay (s)"],
  ["periodSeconds", "Period (s)"],
  ["timeoutSeconds", "Timeout (s)"],
  ["failureThreshold", "Failure threshold"],
] as const;

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function probeTypeOf(probe: Record<string, unknown>): ProbeType {
  if ("httpGet" in probe) return "httpGet";
  if ("tcpSocket" in probe) return "tcpSocket";
  if ("exec" in probe) return "exec";
  return "none";
}

/**
 * Readiness/liveness/startup probe editor. Writes the full probe object at
 * `containerPath(kind) + ["<probeKind>Probe"]` (or clears the key entirely
 * for "None") rather than the single free-text path field it replaces, so a
 * TCP-socket or exec probe is representable, not just an httpGet path.
 * `guided-resource-model.ts`'s `validateGuidedResource` blocks submit when a
 * configured httpGet/tcpSocket probe has no valid port.
 */
export function ProbeFields({
  form,
  kind,
}: GuidedSectionProps & { kind: ProbeKind }) {
  const { value, containerPath, set, setNumber, errors } = form;
  if (!containerPath) return null;

  const probeKey = `${kind}Probe`;
  const probePath: ManifestPath = [...containerPath, probeKey];
  const probe = asRecord(manifestValue(value, probePath));
  const type = probeTypeOf(probe);

  const setType = (nextType: ProbeType) => {
    if (nextType === "none") {
      set(probePath, undefined);
      return;
    }
    const common: Record<string, unknown> = {};
    for (const [field] of PROBE_NUMBER_FIELDS) {
      if (probe[field] !== undefined) common[field] = probe[field];
    }
    if (nextType === "httpGet") {
      set(probePath, { ...common, httpGet: { path: "/", port: "" } });
    } else if (nextType === "tcpSocket") {
      set(probePath, { ...common, tcpSocket: { port: "" } });
    } else {
      set(probePath, { ...common, exec: { command: [""] } });
    }
  };

  const httpGet = asRecord(probe.httpGet);
  const tcpSocket = asRecord(probe.tcpSocket);
  const execCommand = Array.isArray(asRecord(probe.exec).command)
    ? (asRecord(probe.exec).command as unknown[])
    : [];

  return (
    <div className="space-y-3 rounded-md border border-border p-3">
      <p className="text-sm font-medium text-foreground">{PROBE_LABEL[kind]}</p>
      <Field label="Type">
        <Select
          value={type}
          onChange={(event) => setType(event.target.value as ProbeType)}
        >
          <option value="none">None</option>
          <option value="httpGet">HTTP GET</option>
          <option value="tcpSocket">TCP socket</option>
          <option value="exec">Exec command</option>
        </Select>
      </Field>

      {type === "httpGet" && (
        <>
          <Field label="Path">
            <Input
              value={String(httpGet.path ?? "")}
              onChange={(event) =>
                set([...probePath, "httpGet", "path"], event.target.value)
              }
            />
          </Field>
          <Field label="Port" error={errors[probeKey]}>
            <Input
              value={String(httpGet.port ?? "")}
              onChange={(event) =>
                set(
                  [...probePath, "httpGet", "port"],
                  probePortValue(event.target.value),
                )
              }
            />
          </Field>
          <Field label="Scheme">
            <Select
              value={String(httpGet.scheme ?? "HTTP")}
              onChange={(event) =>
                set([...probePath, "httpGet", "scheme"], event.target.value)
              }
            >
              <option value="HTTP">HTTP</option>
              <option value="HTTPS">HTTPS</option>
            </Select>
          </Field>
        </>
      )}

      {type === "tcpSocket" && (
        <Field label="Port" error={errors[probeKey]}>
          <Input
            value={String(tcpSocket.port ?? "")}
            onChange={(event) =>
              set(
                [...probePath, "tcpSocket", "port"],
                probePortValue(event.target.value),
              )
            }
          />
        </Field>
      )}

      {type === "exec" && (
        <Field
          label="Command"
          description="One argument per line."
          error={errors[probeKey]}
        >
          <Textarea
            value={execCommand.join("\n")}
            onChange={(event) =>
              set(
                [...probePath, "exec", "command"],
                event.target.value
                  .split("\n")
                  .map((line) => line.trim())
                  .filter(Boolean),
              )
            }
            rows={3}
          />
        </Field>
      )}

      {type !== "none" && (
        <div className="grid grid-cols-2 gap-3">
          {PROBE_NUMBER_FIELDS.map(([field, label]) => (
            <Field key={field} label={label}>
              <Input
                type="number"
                value={probe[field] === undefined ? "" : String(probe[field])}
                onChange={(event) =>
                  setNumber([...probePath, field], event.target.value)
                }
              />
            </Field>
          ))}
        </div>
      )}
    </div>
  );
}
