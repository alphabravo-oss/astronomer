import { FileText, Terminal } from "lucide-react";

import { BareButton } from "@/components/form/bare-button";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  environmentSummary,
  lastTermination,
  probeSummary,
  stateEntry,
  type ContainerRow,
} from "@/components/resources/pod-overview-model";
import { cn, formatRelativeTime } from "@/lib/utils";

function RuntimeDetails({ row }: { row: ContainerRow }) {
  const container = row.spec;
  const requests = Object.entries(container.resources?.requests ?? {}).map(
    ([key, value]) => `${key}: ${value}`,
  );
  const limits = Object.entries(container.resources?.limits ?? {}).map(
    ([key, value]) => `${key}: ${value}`,
  );
  const ports = (container.ports ?? []).map(
    (port) =>
      `${port.name ? `${port.name} · ` : ""}${port.containerPort ?? "?"}/${port.protocol ?? "TCP"}`,
  );
  const mounts = (container.volumeMounts ?? []).map(
    (mount) =>
      `${mount.name ?? "?"} → ${mount.mountPath ?? "?"}${mount.readOnly ? " · read-only" : ""}${mount.subPath ? ` · ${mount.subPath}` : ""}`,
  );
  const env = environmentSummary(container);
  const probes = [
    ["Readiness", probeSummary(container.readinessProbe)],
    ["Liveness", probeSummary(container.livenessProbe)],
    ["Startup", probeSummary(container.startupProbe)],
  ].filter((entry): entry is [string, string] => Boolean(entry[1]));

  return (
    <details className="group border-t border-border/70 bg-muted/10 px-4 py-3">
      <summary className="cursor-pointer select-none text-xs font-medium text-muted-foreground hover:text-foreground">
        Runtime configuration
      </summary>
      <div className="mt-4 grid gap-5 md:grid-cols-2 xl:grid-cols-3">
        <RuntimeList title="Requests" values={requests} />
        <RuntimeList title="Limits" values={limits} />
        <RuntimeList title="Ports" values={ports} />
        <RuntimeList title="Volume mounts" values={mounts} />
        <RuntimeList title="Environment" values={env} />
        <RuntimeList
          title="Probes"
          values={probes.map(([name, value]) => `${name}: ${value}`)}
        />
      </div>
    </details>
  );
}

function RuntimeList({ title, values }: { title: string; values: string[] }) {
  return (
    <div>
      <h4 className="mb-1.5 text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </h4>
      {values.length === 0 ? (
        <p className="text-xs text-muted-foreground/70">Not configured</p>
      ) : (
        <ul className="space-y-1">
          {values.map((value, index) => (
            <li
              key={`${value}-${index}`}
              className="break-all font-mono text-xs text-foreground"
            >
              {value}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ContainerCard({
  row,
  openLogs,
  openExec,
  logsAllowed,
  execAllowed,
  logsReason,
  execReason,
}: {
  row: ContainerRow;
  openLogs: () => void;
  openExec: () => void;
  logsAllowed: boolean;
  execAllowed: boolean;
  logsReason: string;
  execReason: string;
}) {
  const current = stateEntry(row.status);
  const previous = lastTermination(row.status);
  const running = current.state === "Running";
  return (
    <article className="overflow-hidden rounded-lg border border-border bg-card shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-3 px-4 py-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-mono text-sm font-semibold text-foreground">
              {row.spec.name || "Unnamed container"}
            </h3>
            <span className="rounded-full bg-muted px-2 py-0.5 text-2xs font-medium text-muted-foreground">
              {row.kind}
            </span>
            <StatusBadge status={current.detail?.reason || current.state} />
          </div>
          <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
            {row.spec.image || row.status?.image || "Image unavailable"}
          </p>
        </div>
        <div className="flex items-center gap-1.5">
          <BareButton
            type="button"
            onClick={openLogs}
            disabled={!logsAllowed}
            disabledReason={logsAllowed ? "Open container logs" : logsReason}
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            <FileText className="h-3.5 w-3.5" /> Logs
          </BareButton>
          <BareButton
            type="button"
            onClick={openExec}
            disabled={!execAllowed || !running}
            disabledReason={
              !running
                ? "Container must be running."
                : execAllowed
                  ? "Execute a shell"
                  : execReason
            }
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Terminal className="h-3.5 w-3.5" /> Shell
          </BareButton>
        </div>
      </div>

      <div className="grid gap-px border-t border-border bg-border sm:grid-cols-4">
        <ContainerFact label="Ready" value={row.status?.ready ? "Yes" : "No"} />
        <ContainerFact
          label="Started"
          value={row.status?.started === false ? "No" : running ? "Yes" : "—"}
        />
        <ContainerFact
          label="Restarts"
          value={String(row.status?.restartCount ?? 0)}
          warning={(row.status?.restartCount ?? 0) > 0}
        />
        <ContainerFact
          label="Since"
          value={
            current.detail?.startedAt
              ? formatRelativeTime(current.detail.startedAt)
              : "—"
          }
        />
      </div>

      {(current.detail?.reason || current.detail?.message || previous) && (
        <div className="space-y-2 border-t border-border px-4 py-3 text-xs">
          {(current.detail?.reason || current.detail?.message) && (
            <p>
              <span className="font-medium text-foreground">
                Current: {current.detail.reason || current.state}
              </span>
              {current.detail.message ? (
                <span className="ml-2 text-muted-foreground">
                  {current.detail.message}
                </span>
              ) : null}
            </p>
          )}
          {previous && (
            <p className="text-muted-foreground">
              <span className="font-medium text-foreground">Previous:</span>{" "}
              {previous.reason || "Terminated"}
              {previous.exitCode != null ? ` · exit ${previous.exitCode}` : ""}
              {previous.finishedAt
                ? ` · ${formatRelativeTime(previous.finishedAt)}`
                : ""}
              {previous.message ? ` · ${previous.message}` : ""}
            </p>
          )}
        </div>
      )}
      <RuntimeDetails row={row} />
    </article>
  );
}

function ContainerFact({
  label,
  value,
  warning,
}: {
  label: string;
  value: string;
  warning?: boolean;
}) {
  return (
    <div className="bg-card px-4 py-2.5">
      <p className="text-2xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p
        className={cn(
          "mt-0.5 text-xs font-medium text-foreground",
          warning && "text-status-warning",
        )}
      >
        {value}
      </p>
    </div>
  );
}
