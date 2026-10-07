import { useMemo } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  AlertTriangle,
  CheckCircle2,
  CircleDot,
  LockKeyhole,
} from "lucide-react";

import type { K8sObject } from "@/components/resources/resource-detail-model";
import { ContainerCard } from "@/components/resources/pod-container-card";
import {
  containerRows,
  diagnosticForPod,
  referenceRows,
} from "@/components/resources/pod-overview-model";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";
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
    <div className="space-y-(--gap-section)">
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
