import { Link as RouterLink } from "@tanstack/react-router";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import {
  useClusterConditionRemediation,
  useClusterEvents,
} from "@/lib/hooks/clusters";
import { useAnomalyBaselines } from "@/lib/hooks/alerting";
import { type ServiceMeshKind } from "@/lib/api/cluster-service-mesh";
import { MetricCard } from "@/components/ui/metric-card";
import { StatusBadge } from "@/components/ui/status-badge";
import { QueryStates } from "@/components/ui/query-states";
import { Tooltip } from "@/components/ui/tooltip";
import { clusterLifecycleDetail } from "./-cluster-status";
import {
  formatRelativeTime,
  distributionDisplayName,
  formatK8sVersion,
  capitalize,
} from "@/lib/utils";
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  CircleHelp,
  ShieldAlert,
  Package,
} from "lucide-react";
import type { Cluster, ClusterCondition } from "@/types";

type OverviewMetric = { value: string | number; subtitle?: string };

// ── Cluster conditions ──────────────────────────────────────────────────────
//
// Renders the kubectl-style condition pills under the cluster header. Each
// chip shows the condition type + a coloured indicator; hover reveals the
// reason, message, and how long the condition has been in its current state.

const CONDITION_LABELS: Record<string, string> = {
  Connected: "Connected",
  AgentReachable: "Agent Reachable",
  GatewayAPISupported: "Gateway API",
  DeliveryReady: "Delivery ready",
  MetricsAvailable: "Metrics Available",
};

function relativeAge(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const diff = Math.max(0, Date.now() - t);
  const m = Math.floor(diff / 60_000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

// Capability conditions report what the cluster *can* do, not whether it is
// healthy. Absence is the normal case — most clusters have never installed the
// Gateway API CRDs — so a False here is not a finding and must not render as a
// red failure next to genuine ones like Connected=False. Drop them; the detail
// stays available on the conditions list.
const CAPABILITY_CONDITIONS = new Set(["GatewayAPISupported"]);

export function isNoisyCapabilityCondition(c: ClusterCondition): boolean {
  return CAPABILITY_CONDITIONS.has(c.type) && c.status !== "True";
}

export function ClusterConditionsBar({
  conditions,
}: {
  conditions: ClusterCondition[];
}) {
  const visible = conditions.filter((c) => !isNoisyCapabilityCondition(c));
  if (visible.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {visible.map((c) => {
        const label = CONDITION_LABELS[c.type] || c.type;
        let tone = "";
        let Icon = CircleHelp;
        switch (c.status) {
          case "True":
            tone =
              "bg-status-success/10 text-status-success border-status-success/20";
            Icon = CheckCircle2;
            break;
          case "False":
            tone =
              "bg-status-error/10 text-status-error border-status-error/20";
            Icon = XCircle;
            break;
          default:
            tone = "bg-muted text-muted-foreground border-border";
            Icon = CircleHelp;
        }
        const tooltip = [
          `${c.reason || c.status}`,
          c.message,
          `For ${relativeAge(c.last_transition_time)}`,
        ]
          .filter(Boolean)
          .join(" — ");
        return (
          <Tooltip key={c.type} content={tooltip}>
            <span
              className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded-sm text-xs border ${tone}`}
            >
              <Icon className="h-3 w-3" />
              {label}
            </span>
          </Tooltip>
        );
      })}
    </div>
  );
}

// ClusterRemediationFooter — shows the most recent action the
// cluster-condition reconciler took for this cluster. Hidden when
// there's no history yet (the common case for green clusters).
export function ClusterRemediationFooter({ clusterId }: { clusterId: string }) {
  const { data } = useClusterConditionRemediation(clusterId);
  if (!data || data.length === 0) return null;
  const latest = data[0];
  const tone =
    latest.outcome === "success"
      ? "text-status-success"
      : latest.outcome === "failed"
        ? "text-status-error"
        : "text-muted-foreground";
  return (
    <Tooltip content={latest.error || latest.action}>
      <div className="text-11 text-muted-foreground pt-1">
        Last remediation:{" "}
        <span className={tone}>
          {latest.action} — {latest.outcome}
        </span>
        <span className="text-border"> · </span>
        <span>{relativeAge(latest.attempted_at)} ago</span>
      </div>
    </Tooltip>
  );
}

// MeshHeaderBadge — compact "Istio" / "Linkerd" pill rendered next to the
// cluster status badge. Links to the per-cluster service-mesh tab so a single
// click drills into the full tile.
//
// Most clusters run no mesh, and the detector returns an "unknown" stub before
// it has ever run. Both used to render as `mesh: —`: a permanent header chip
// whose entire content was "we looked and found nothing". Render nothing
// instead — the mesh tab is still there for anyone who wants to check.
const MESH_LABELS: Partial<Record<ServiceMeshKind, string>> = {
  istio: "Istio",
  linkerd: "Linkerd",
  kuma: "Kuma",
  cilium: "Cilium",
};

export function MeshHeaderBadge({
  clusterId,
  mesh,
}: {
  clusterId: string;
  mesh: ServiceMeshKind;
}) {
  const label = MESH_LABELS[mesh];
  if (!label) return null;
  const tone =
    mesh === "istio"
      ? "border-status-info/30 text-status-info bg-status-info/10"
      : mesh === "linkerd"
        ? "border-status-success/30 text-status-success bg-status-success/10"
        : "border-border text-muted-foreground bg-muted/30";
  return (
    <Tooltip content="Service mesh detection">
      <RouterLink
        to="/dashboard/clusters/$id/service-mesh"
        params={{ id: clusterId }}
        className={`inline-flex items-center px-2 py-0.5 rounded-sm text-xs font-medium border ${tone} hover:opacity-80 transition-opacity`}
      >
        mesh: {label}
      </RouterLink>
    </Tooltip>
  );
}

// ── Anomaly baselines panel (T7.2) ───────────────────────────────────────
//
// The nightly baseline job computes a per-metric
// rolling mean + stddev per cluster. Until now those rows lived in
// the DB with nothing rendering them. The panel surfaces the top 5
// metrics by sample count (the most-observed → most-trustworthy)
// with mean ± stddev so an operator can sanity-check what the
// platform considers "normal" for the cluster. Read-only.
export function AnomalyBaselinesPanel({ clusterId }: { clusterId: string }) {
  const query = useAnomalyBaselines({ clusterId, limit: 5 });
  return (
    <div>
      <h3 className="text-sm font-medium text-muted-foreground mb-3">
        Anomaly Baselines
      </h3>
      <QueryStates
        query={query}
        loadingTitle="Loading baselines…"
        isEmpty={(data) => data.length === 0}
        empty={
          <div className="rounded-lg border border-dashed border-border p-4 text-xs text-muted-foreground">
            Baselines appear after 24 hours of metrics.
          </div>
        }
      >
        {(data) => {
          const rows = data.slice(0, 5);
          return (
            <div className="rounded-lg border border-border overflow-hidden">
              <Table className="w-full text-sm">
                <TableHeader className="bg-muted/30 text-xs text-muted-foreground">
                  <TableRow>
                    <TableHead className="px-3 py-2 text-left font-medium">
                      Metric
                    </TableHead>
                    <TableHead className="px-3 py-2 text-right font-medium">
                      Mean
                    </TableHead>
                    <TableHead className="px-3 py-2 text-right font-medium">
                      Stddev
                    </TableHead>
                    <TableHead className="px-3 py-2 text-right font-medium">
                      Samples
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody className="divide-y divide-border">
                  {rows.map((b) => (
                    <TableRow key={b.id}>
                      <TableCell className="px-3 py-2 font-mono text-xs text-foreground">
                        {b.metric}
                      </TableCell>
                      <TableCell className="px-3 py-2 text-right tabular-nums text-foreground">
                        {b.mean.toFixed(2)}
                      </TableCell>
                      <TableCell className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                        ±{b.stddev.toFixed(2)}
                      </TableCell>
                      <TableCell className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                        {b.sampleCount}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          );
        }}
      </QueryStates>
    </div>
  );
}

export function clusterOverviewMetadata(cluster: {
  distribution?: string | null;
  kubernetesVersion?: string | null;
  environment?: string | null;
  registrationPhase: Cluster["registrationPhase"];
}) {
  const lifecycle = clusterLifecycleDetail(cluster);
  return [
    {
      label: "Distribution",
      value: distributionDisplayName(cluster.distribution ?? ""),
    },
    { label: "Version", value: formatK8sVersion(cluster.kubernetesVersion) },
    { label: "Environment", value: capitalize(cluster.environment ?? "") },
    ...(lifecycle ? [{ label: "Lifecycle", value: lifecycle }] : []),
  ];
}

export function ClusterHealthComponents({ cluster }: { cluster: Cluster }) {
  return (
    <>
      {/* Health Components */}
      {cluster.health?.components && cluster.health.components.length > 0 && (
        <div>
          <h3 className="text-sm font-medium text-muted-foreground mb-3">
            Health Components
          </h3>
          <div className="flex flex-wrap gap-2">
            {cluster.health.components.map((comp) => (
              <div
                key={comp.name}
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-md border border-border bg-card"
              >
                <StatusBadge status={comp.status} size="sm" />
                <span className="text-sm text-foreground">{comp.name}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  );
}

export function ClusterPlatformHealthRow({
  clusterId,
  cluster,
  criticalMetric,
  highMetric,
  toolsMetric,
}: {
  clusterId: string;
  cluster: Cluster;
  criticalMetric: OverviewMetric;
  highMetric: OverviewMetric;
  toolsMetric: OverviewMetric;
}) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
      <RouterLink
        to="/dashboard/clusters/$id/image-scans"
        params={{ id: clusterId }}
        className="contents"
      >
        <MetricCard
          title="Critical CVEs"
          value={criticalMetric.value}
          subtitle={criticalMetric.subtitle}
          icon={<ShieldAlert className="h-4 w-4" />}
          className={
            typeof criticalMetric.value === "number" && criticalMetric.value > 0
              ? "cursor-pointer hover:border-status-error/50 transition-colors"
              : "cursor-pointer hover:border-muted-foreground/50 transition-colors"
          }
        />
      </RouterLink>
      <RouterLink
        to="/dashboard/clusters/$id/image-scans"
        params={{ id: clusterId }}
        className="contents"
      >
        <MetricCard
          title="High CVEs"
          value={highMetric.value}
          subtitle={highMetric.subtitle}
          icon={<ShieldAlert className="h-4 w-4" />}
          className="cursor-pointer hover:border-muted-foreground/50 transition-colors"
        />
      </RouterLink>
      <RouterLink
        to="/dashboard/clusters/$id/tools"
        params={{ id: clusterId }}
        className="contents"
      >
        <MetricCard
          title="Tools"
          value={toolsMetric.value}
          subtitle={toolsMetric.subtitle}
          icon={<Package className="h-4 w-4" />}
          className="cursor-pointer hover:border-muted-foreground/50 transition-colors"
        />
      </RouterLink>
      <MetricCard
        title="Agent"
        value={cluster.agentVersion || "—"}
        subtitle={
          cluster.lastHeartbeat
            ? `heartbeat ${formatRelativeTime(cluster.lastHeartbeat)}`
            : "never connected"
        }
        icon={<Activity className="h-4 w-4" />}
      />
    </div>
  );
}

export function ClusterRecentEvents({
  events,
}: {
  events: NonNullable<ReturnType<typeof useClusterEvents>["data"]> | undefined;
}) {
  return (
    <>
      {/* Recent Events */}
      <div>
        <h3 className="text-sm font-medium text-muted-foreground mb-3">
          Recent Events
        </h3>
        <div className="rounded-lg border border-border overflow-hidden">
          {events && events.length > 0 ? (
            <div className="divide-y divide-border">
              {events.slice(0, 8).map((event) => (
                <div
                  key={event.id}
                  className="flex items-center gap-3 px-4 py-2.5"
                >
                  {event.type === "Warning" ? (
                    <AlertTriangle className="h-3.5 w-3.5 text-status-warning shrink-0" />
                  ) : (
                    <Activity className="h-3.5 w-3.5 text-status-info shrink-0" />
                  )}
                  <span className="text-sm text-foreground flex-1 truncate">
                    {event.message}
                  </span>
                  <span className="text-xs text-muted-foreground shrink-0">
                    {formatRelativeTime(event.lastTimestamp)}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <div className="flex items-center justify-center py-8 text-sm text-muted-foreground">
              No recent events
            </div>
          )}
        </div>
      </div>
    </>
  );
}
