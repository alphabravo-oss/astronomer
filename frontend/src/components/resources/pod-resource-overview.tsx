import { useMemo } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  AlertTriangle,
  CheckCircle2,
  CircleDot,
  FileText,
  LockKeyhole,
  Terminal,
} from "lucide-react";

import type {
  ContainerSpec,
  ContainerStateDetail,
  ContainerStatus,
  K8sObject,
} from "@/components/resources/resource-detail-model";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";
import { StatusBadge } from "@/components/ui/status-badge";
import { MetricCard } from "@/components/ui/metric-card";
import { detailHref } from "@/lib/k8s-paths";
import {
  permissionDeniedReason,
  useClusterResourcePermission,
} from "@/lib/permission-hooks";
import { useWindowManagerStore } from "@/lib/window-manager-store";
import { cn, formatRelativeTime } from "@/lib/utils";

interface PodResourceOverviewProps {
  obj: K8sObject;
  clusterId: string;
  namespace: string;
  name: string;
}

interface ContainerRow {
  spec: ContainerSpec;
  status?: ContainerStatus;
  kind: "Application" | "Init" | "Ephemeral";
}

interface Diagnostic {
  title: string;
  message?: string;
  tone: "healthy" | "warning" | "error";
}

function stateEntry(status?: ContainerStatus): {
  state: string;
  detail?: ContainerStateDetail;
} {
  if (!status?.state) return { state: "Unknown" };
  for (const name of ["waiting", "terminated", "running"]) {
    const detail = status.state[name];
    if (detail) {
      return {
        state: name.charAt(0).toUpperCase() + name.slice(1),
        detail,
      };
    }
  }
  return { state: "Unknown" };
}

function lastTermination(
  status?: ContainerStatus,
): ContainerStateDetail | undefined {
  return status?.lastState?.terminated;
}

function diagnosticForPod(obj: K8sObject): Diagnostic {
  const status = obj.status ?? {};
  const allStatuses = [
    ...(status.initContainerStatuses ?? []),
    ...(status.containerStatuses ?? []),
    ...(status.ephemeralContainerStatuses ?? []),
  ];

  for (const container of allStatuses) {
    const current = stateEntry(container);
    if (current.state === "Waiting" && current.detail?.reason) {
      return {
        title: `${container.name ?? "Container"}: ${current.detail.reason}`,
        message: current.detail.message,
        tone:
          current.detail.reason === "CrashLoopBackOff" ||
          current.detail.reason === "ImagePullBackOff" ||
          current.detail.reason === "ErrImagePull"
            ? "error"
            : "warning",
      };
    }
    if (
      current.state === "Terminated" &&
      (current.detail?.exitCode ?? 0) !== 0
    ) {
      return {
        title: `${container.name ?? "Container"}: ${current.detail?.reason || `Exited ${current.detail?.exitCode}`}`,
        message: current.detail?.message,
        tone: "error",
      };
    }
    const previous = lastTermination(container);
    if (
      (container.restartCount ?? 0) > 0 &&
      (previous?.reason === "OOMKilled" || (previous?.exitCode ?? 0) !== 0)
    ) {
      return {
        title: `${container.name ?? "Container"} restarted after ${previous?.reason || `exit ${previous?.exitCode}`}`,
        message: previous?.message,
        tone: previous?.reason === "OOMKilled" ? "error" : "warning",
      };
    }
  }

  const importantConditions = new Set([
    "PodScheduled",
    "Initialized",
    "ContainersReady",
    "Ready",
  ]);
  const failedCondition = (status.conditions ?? []).find(
    (condition) =>
      importantConditions.has(String(condition.type ?? "")) &&
      String(condition.status ?? "") !== "True",
  );
  if (failedCondition) {
    return {
      title: String(
        failedCondition.reason || failedCondition.type || "Pod is not ready",
      ),
      message: String(failedCondition.message || ""),
      tone:
        String(failedCondition.type) === "PodScheduled" ? "error" : "warning",
    };
  }
  if (status.reason || status.message) {
    return {
      title: status.reason || status.phase || "Pod needs attention",
      message: status.message,
      tone: status.phase === "Failed" ? "error" : "warning",
    };
  }
  if (status.phase !== "Running" && status.phase !== "Succeeded") {
    return {
      title: `Pod is ${status.phase || "not ready"}`,
      tone: status.phase === "Failed" ? "error" : "warning",
    };
  }
  return {
    title:
      status.phase === "Succeeded"
        ? "Pod completed successfully"
        : "Pod is healthy and ready",
    tone: "healthy",
  };
}

function statusByName(statuses: ContainerStatus[] | undefined) {
  return new Map((statuses ?? []).map((status) => [status.name ?? "", status]));
}

function containerRows(obj: K8sObject): ContainerRow[] {
  const spec = obj.spec ?? {};
  const status = obj.status ?? {};
  const appStatuses = statusByName(status.containerStatuses);
  const initStatuses = statusByName(status.initContainerStatuses);
  const ephemeralStatuses = statusByName(status.ephemeralContainerStatuses);
  return [
    ...(spec.initContainers ?? []).map((container) => ({
      spec: container,
      status: initStatuses.get(container.name ?? ""),
      kind: "Init" as const,
    })),
    ...(spec.containers ?? []).map((container) => ({
      spec: container,
      status: appStatuses.get(container.name ?? ""),
      kind: "Application" as const,
    })),
    ...(spec.ephemeralContainers ?? []).map((container) => ({
      spec: container,
      status: ephemeralStatuses.get(container.name ?? ""),
      kind: "Ephemeral" as const,
    })),
  ];
}

function probeSummary(probe?: Record<string, unknown>): string | undefined {
  if (!probe) return undefined;
  const handler = ["httpGet", "tcpSocket", "grpc", "exec"].find(
    (key) => probe[key],
  );
  const timing = [
    probe.initialDelaySeconds != null
      ? `delay ${String(probe.initialDelaySeconds)}s`
      : "",
    probe.periodSeconds != null ? `every ${String(probe.periodSeconds)}s` : "",
    probe.timeoutSeconds != null
      ? `timeout ${String(probe.timeoutSeconds)}s`
      : "",
  ].filter(Boolean);
  return [handler || "configured", ...timing].join(" · ");
}

function environmentSummary(container: ContainerSpec): string[] {
  const rows: string[] = [];
  for (const env of container.env ?? []) {
    if (!env.name) continue;
    const source = env.valueFrom ?? {};
    const secret = source.secretKeyRef as
      { name?: string; key?: string } | undefined;
    const config = source.configMapKeyRef as
      { name?: string; key?: string } | undefined;
    const field = source.fieldRef as { fieldPath?: string } | undefined;
    const resource = source.resourceFieldRef as
      { resource?: string } | undefined;
    if (secret) {
      rows.push(
        `${env.name} ← Secret ${secret.name ?? "?"}/${secret.key ?? "?"}`,
      );
    } else if (config) {
      rows.push(
        `${env.name} ← ConfigMap ${config.name ?? "?"}/${config.key ?? "?"}`,
      );
    } else if (field) {
      rows.push(`${env.name} ← ${field.fieldPath ?? "pod field"}`);
    } else if (resource) {
      rows.push(`${env.name} ← ${resource.resource ?? "container resource"}`);
    } else {
      rows.push(`${env.name}=${env.value ?? ""}`);
    }
  }
  for (const source of container.envFrom ?? []) {
    const secret = source.secretRef as { name?: string } | undefined;
    const config = source.configMapRef as { name?: string } | undefined;
    if (secret) rows.push(`All keys from Secret ${secret.name ?? "?"}`);
    if (config) rows.push(`All keys from ConfigMap ${config.name ?? "?"}`);
  }
  return rows;
}

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

function ContainerCard({
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
          <button
            type="button"
            onClick={openLogs}
            disabled={!logsAllowed}
            title={logsAllowed ? "Open container logs" : logsReason}
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            <FileText className="h-3.5 w-3.5" /> Logs
          </button>
          <button
            type="button"
            onClick={openExec}
            disabled={!execAllowed || !running}
            title={
              !running
                ? "Container must be running."
                : execAllowed
                  ? "Execute a shell"
                  : execReason
            }
            className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Terminal className="h-3.5 w-3.5" /> Shell
          </button>
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

function referenceRows(obj: K8sObject) {
  const spec = obj.spec ?? {};
  const refs: Array<{
    type: string;
    resourceType: string;
    name: string;
    detail: string;
    secret?: boolean;
  }> = [];
  const add = (
    type: string,
    resourceType: string,
    name: unknown,
    detail: string,
    secret = false,
  ) => {
    if (typeof name !== "string" || !name) return;
    if (
      refs.some((ref) => ref.resourceType === resourceType && ref.name === name)
    )
      return;
    refs.push({ type, resourceType, name, detail, secret });
  };

  add("Node", "nodes", spec.nodeName, "Scheduling target");
  add(
    "Service account",
    "serviceaccounts",
    spec.serviceAccountName,
    "Pod identity",
  );
  for (const pullSecret of spec.imagePullSecrets ?? []) {
    add("Secret", "secrets", pullSecret.name, "Image pull credentials", true);
  }
  for (const volume of spec.volumes ?? []) {
    const pvc = volume.persistentVolumeClaim as
      { claimName?: string } | undefined;
    const secret = volume.secret as { secretName?: string } | undefined;
    const config = volume.configMap as { name?: string } | undefined;
    add(
      "PersistentVolumeClaim",
      "persistentvolumeclaims",
      pvc?.claimName,
      `Volume ${volume.name ?? ""}`,
    );
    add("ConfigMap", "configmaps", config?.name, `Volume ${volume.name ?? ""}`);
    add(
      "Secret",
      "secrets",
      secret?.secretName,
      `Volume ${volume.name ?? ""}`,
      true,
    );
    const projected = volume.projected as
      { sources?: Array<Record<string, unknown>> } | undefined;
    for (const source of projected?.sources ?? []) {
      const projectedSecret = source.secret as { name?: string } | undefined;
      const projectedConfig = source.configMap as { name?: string } | undefined;
      add(
        "Secret",
        "secrets",
        projectedSecret?.name,
        `Projected volume ${volume.name ?? ""}`,
        true,
      );
      add(
        "ConfigMap",
        "configmaps",
        projectedConfig?.name,
        `Projected volume ${volume.name ?? ""}`,
      );
    }
  }
  for (const container of [
    ...(spec.initContainers ?? []),
    ...(spec.containers ?? []),
    ...(spec.ephemeralContainers ?? []),
  ]) {
    for (const env of container.env ?? []) {
      const secret = env.valueFrom?.secretKeyRef as
        { name?: string } | undefined;
      const config = env.valueFrom?.configMapKeyRef as
        { name?: string } | undefined;
      add(
        "Secret",
        "secrets",
        secret?.name,
        `Environment · ${container.name}`,
        true,
      );
      add(
        "ConfigMap",
        "configmaps",
        config?.name,
        `Environment · ${container.name}`,
      );
    }
    for (const source of container.envFrom ?? []) {
      const secret = source.secretRef as { name?: string } | undefined;
      const config = source.configMapRef as { name?: string } | undefined;
      add(
        "Secret",
        "secrets",
        secret?.name,
        `Environment · ${container.name}`,
        true,
      );
      add(
        "ConfigMap",
        "configmaps",
        config?.name,
        `Environment · ${container.name}`,
      );
    }
  }
  return refs;
}

export function PodResourceOverview({
  obj,
  clusterId,
  namespace,
  name,
}: PodResourceOverviewProps) {
  const logsPermission = useClusterResourcePermission(
    clusterId,
    "pods",
    "logs",
  );
  const execPermission = useClusterResourcePermission(
    clusterId,
    "pods",
    "exec",
  );
  const secretPermission = useClusterResourcePermission(
    clusterId,
    "secrets",
    "read",
  );
  const rows = useMemo(() => containerRows(obj), [obj]);
  const diagnostic = useMemo(() => diagnosticForPod(obj), [obj]);
  const references = useMemo(() => referenceRows(obj), [obj]);
  const status = obj.status ?? {};
  const spec = obj.spec ?? {};
  const restartCount = rows.reduce(
    (total, row) => total + (row.status?.restartCount ?? 0),
    0,
  );
  const ready = (status.containerStatuses ?? []).filter(
    (container) => container.ready,
  ).length;
  const desired = spec.containers?.length ?? 0;
  const open = (kind: "logs" | "exec", container: string) =>
    useWindowManagerStore.getState().addTab({
      kind,
      clusterId,
      namespace,
      pod: name,
      container,
    });

  return (
    <div className="space-y-6">
      <div
        className={cn(
          "flex items-start gap-3 rounded-lg border px-4 py-3",
          diagnostic.tone === "healthy" &&
            "border-status-success/30 bg-status-success/5",
          diagnostic.tone === "warning" &&
            "border-status-warning/30 bg-status-warning/5",
          diagnostic.tone === "error" &&
            "border-status-error/30 bg-status-error/5",
        )}
        role="status"
      >
        {diagnostic.tone === "healthy" ? (
          <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-status-success" />
        ) : (
          <AlertTriangle
            className={cn(
              "mt-0.5 h-5 w-5 shrink-0",
              diagnostic.tone === "error"
                ? "text-status-error"
                : "text-status-warning",
            )}
          />
        )}
        <div className="min-w-0">
          <p className="text-sm font-semibold text-foreground">
            {diagnostic.title}
          </p>
          {diagnostic.message ? (
            <p className="mt-1 break-words text-xs text-muted-foreground">
              {diagnostic.message}
            </p>
          ) : null}
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <SummaryCard label="Phase" value={status.phase || "Unknown"} />
        <SummaryCard label="Ready" value={`${ready}/${desired}`} />
        <SummaryCard
          label="Restarts"
          value={String(restartCount)}
          warning={restartCount > 0}
        />
        <SummaryCard label="QoS class" value={status.qosClass || "—"} />
        <SummaryCard label="Pod IP" value={status.podIP || "—"} mono />
        <SummaryCard label="Host IP" value={status.hostIP || "—"} mono />
        <SummaryCard
          label="Node"
          value={spec.nodeName || "Unscheduled"}
          mono
          href={
            spec.nodeName
              ? detailHref(clusterId, "nodes", undefined, spec.nodeName)
              : undefined
          }
        />
        <SummaryCard
          label="Service account"
          value={spec.serviceAccountName || "default"}
          mono
          href={detailHref(
            clusterId,
            "serviceaccounts",
            namespace,
            spec.serviceAccountName || "default",
          )}
        />
      </div>

      <Section title={`Containers (${rows.length})`}>
        <div className="space-y-4">
          {rows.length === 0 ? (
            <p className="text-xs text-muted-foreground">No containers.</p>
          ) : (
            rows.map((row) => (
              <ContainerCard
                key={`${row.kind}/${row.spec.name}`}
                row={row}
                openLogs={() => open("logs", row.spec.name ?? "")}
                openExec={() => open("exec", row.spec.name ?? "")}
                logsAllowed={logsPermission.allowed}
                execAllowed={execPermission.allowed}
                logsReason={permissionDeniedReason(logsPermission)}
                execReason={permissionDeniedReason(execPermission)}
              />
            ))
          )}
        </div>
      </Section>

      {references.length > 0 && (
        <Section title="Connected resources">
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
            {references.map((reference) => {
              const restricted = reference.secret && !secretPermission.allowed;
              const namespaced = reference.resourceType !== "nodes";
              const content = (
                <>
                  <span className="flex items-center gap-1.5 text-xs font-medium text-foreground">
                    {restricted ? <LockKeyhole className="h-3 w-3" /> : null}
                    {reference.type}
                  </span>
                  <span className="mt-1 block break-all font-mono text-xs text-foreground">
                    {reference.name}
                  </span>
                  <span className="mt-1 block text-2xs text-muted-foreground">
                    {restricted
                      ? "Value access restricted by RBAC"
                      : reference.detail}
                  </span>
                </>
              );
              return restricted ? (
                <div
                  key={`${reference.resourceType}/${reference.name}`}
                  className="rounded-md border border-border bg-muted/20 px-3 py-2"
                >
                  {content}
                </div>
              ) : (
                <RouterLink
                  key={`${reference.resourceType}/${reference.name}`}
                  to={detailHref(
                    clusterId,
                    reference.resourceType,
                    namespaced ? namespace : undefined,
                    reference.name,
                  )}
                  className="rounded-md border border-border bg-muted/20 px-3 py-2 transition-colors hover:border-primary/40 hover:bg-accent"
                >
                  {content}
                </RouterLink>
              );
            })}
          </div>
        </Section>
      )}

      <Section title="Scheduling and runtime">
        <KeyValueTable
          entries={[
            ["restart policy", spec.restartPolicy || "Always"],
            ["scheduler", spec.schedulerName || "default-scheduler"],
            ["priority class", spec.priorityClassName || "—"],
            ["DNS policy", spec.dnsPolicy || "—"],
            ["host network", spec.hostNetwork ? "Yes" : "No"],
            [
              "started",
              status.startTime ? formatRelativeTime(status.startTime) : "—",
            ],
          ]}
        />
      </Section>
    </div>
  );
}

function SummaryCard({
  label,
  value,
  warning,
  mono,
  href,
}: {
  label: string;
  value: string;
  warning?: boolean;
  mono?: boolean;
  href?: string;
}) {
  return (
    <MetricCard
      dense
      label={label}
      icon={<CircleDot className="h-3 w-3" />}
      value={mono ? <span className="font-mono text-xs">{value}</span> : value}
      tone={warning ? "warning" : undefined}
      href={href}
    />
  );
}

