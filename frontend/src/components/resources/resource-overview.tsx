import { formatRelativeTime } from "@/lib/utils";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type {
  ContainerStatus,
  K8sObject,
} from "@/components/resources/resource-detail-model";
import {
  asRecord,
  displayNumber,
} from "@/components/resources/resource-detail-model";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";
import {
  CRDOverview,
  GatewayClassOverview,
  GatewayOverview,
  NetworkPolicyOverview,
  PDBOverview,
  PVOverview,
  ResourceQuotaOverview,
  RoleBindingOverview,
  RoleOverview,
  RouteOverview,
  SecretOverview,
  ServiceAccountOverview,
  StorageClassOverview,
} from "@/components/resources/resource-overview-additional";

export function ResourceOverview({
  obj,
  resourceType,
}: {
  obj?: K8sObject;
  resourceType: string;
}) {
  const meta = obj?.metadata;
  if (!meta) {
    return <p className="text-sm text-muted-foreground">No data.</p>;
  }

  // ponytail: small per-kind branches keyed by resourceType for the few
  // highest-value kinds; everything else falls through to the generic view.
  // Each branch renders its tailored section ABOVE the shared GenericOverview.
  const kindSpecific = (() => {
    switch (resourceType) {
      case "pods":
        return <PodOverview obj={obj!} />;
      case "services":
        return <ServiceOverview obj={obj!} />;
      case "configmaps":
        return <ConfigMapOverview obj={obj!} />;
      case "ingresses":
        return <IngressOverview obj={obj!} />;
      case "persistentvolumeclaims":
        return <PVCOverview obj={obj!} />;
      case "deployments":
      case "statefulsets":
      case "daemonsets":
        return <WorkloadOverview obj={obj!} />;
      case "replicasets":
        return <ReplicaSetOverview obj={obj!} />;
      case "jobs":
        return <JobOverview obj={obj!} />;
      case "cronjobs":
        return <CronJobOverview obj={obj!} />;
      case "hpa":
        return <HPAOverview obj={obj!} />;
      case "secrets":
        return <SecretOverview obj={obj!} />;
      case "persistentvolumes":
        return <PVOverview obj={obj!} />;
      case "networkpolicies":
        return <NetworkPolicyOverview obj={obj!} />;
      case "storageclasses":
        return <StorageClassOverview obj={obj!} />;
      case "k8s-clusterroles":
      case "k8s-roles":
        return <RoleOverview obj={obj!} />;
      case "k8s-clusterrolebindings":
      case "k8s-rolebindings":
        return <RoleBindingOverview obj={obj!} />;
      case "serviceaccounts":
        return <ServiceAccountOverview obj={obj!} />;
      case "poddisruptionbudgets":
        return <PDBOverview obj={obj!} />;
      case "resourcequotas":
        return <ResourceQuotaOverview obj={obj!} />;
      case "crds":
        return <CRDOverview obj={obj!} />;
      case "gateways":
        return <GatewayOverview obj={obj!} />;
      case "gatewayclasses":
        return <GatewayClassOverview obj={obj!} />;
      case "httproutes":
      case "grpcroutes":
      case "tlsroutes":
      case "tcproutes":
      case "udproutes":
        return <RouteOverview obj={obj!} />;
      default:
        return null;
    }
  })();

  if (kindSpecific) {
    return (
      <div className="space-y-6">
        {kindSpecific}
        {/* Tailored overview already summarises status; skip the generic dump. */}
        <GenericOverview
          obj={obj}
          resourceType={resourceType}
          showStatus={false}
        />
      </div>
    );
  }

  return <GenericOverview obj={obj} resourceType={resourceType} showStatus />;
}
function GenericOverview({
  obj,
  resourceType,
  showStatus,
}: {
  obj?: K8sObject;
  resourceType: string;
  showStatus?: boolean;
}) {
  const meta = obj?.metadata;
  if (!meta) {
    return <p className="text-sm text-muted-foreground">No data.</p>;
  }

  // Top-level spec scalars — surfaces .spec for arbitrary CRs / unmapped kinds
  // that have no tailored overview. Skip nested objects/arrays and the fields
  // that kind overviews / actions already handle (replicas/paused/suspend).
  const SPEC_SKIP = new Set(["replicas", "paused", "suspend"]);
  const specEntries = showStatus
    ? Object.entries(asRecord(obj?.spec))
        .filter(
          ([k, v]) =>
            !SPEC_SKIP.has(k) &&
            (typeof v === "string" ||
              typeof v === "number" ||
              typeof v === "boolean"),
        )
        .map(([k, v]) => [k, String(v)] as [string, string])
    : [];

  // Top-level status scalars — surfaces .status for arbitrary CRs / unmapped
  // kinds that have no tailored overview. Conditions live in their own tab.
  const statusEntries = showStatus
    ? Object.entries(asRecord(obj?.status))
        .filter(
          ([k, v]) =>
            k !== "conditions" &&
            (typeof v === "string" ||
              typeof v === "number" ||
              typeof v === "boolean"),
        )
        .map(([k, v]) => [k, String(v)] as [string, string])
    : [];

  const metadataEntries: Array<[string, string]> = [];
  if (meta.name) metadataEntries.push(["name", meta.name]);
  if (meta.namespace) metadataEntries.push(["namespace", meta.namespace]);
  if (meta.uid) metadataEntries.push(["uid", meta.uid]);
  if (meta.creationTimestamp) {
    metadataEntries.push(["created", meta.creationTimestamp]);
    metadataEntries.push(["age", formatRelativeTime(meta.creationTimestamp)]);
  }

  const labels = Object.entries(meta.labels ?? {}) as Array<[string, string]>;
  const annotations = Object.entries(meta.annotations ?? {}) as Array<
    [string, string]
  >;
  const owners = meta.ownerReferences ?? [];

  // ponytail: mask secret 'data' values; only secrets carries this.
  const isSecret = resourceType === "secrets";
  const dataEntries = obj?.data
    ? (Object.entries(obj.data) as Array<[string, string]>)
    : [];

  return (
    <div className="space-y-6">
      <Section title="Metadata">
        <KeyValueTable entries={metadataEntries} />
      </Section>

      <Section title="Labels">
        <KeyValueTable entries={labels} />
      </Section>

      <Section title="Annotations">
        <KeyValueTable entries={annotations} />
      </Section>

      {owners.length > 0 && (
        <Section title="Owner References">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Name</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {owners.map((ref) => (
                <TableRow key={ref.uid || `${ref.kind}/${ref.name}`}>
                  <TableCell className="text-xs">{ref.kind}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {ref.name}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}

      {/* Conditions moved to a dedicated tab (ConditionsTab) — Rancher-style. */}

      {specEntries.length > 0 && (
        <Section title="Spec">
          <KeyValueTable entries={specEntries} />
        </Section>
      )}

      {statusEntries.length > 0 && (
        <Section title="Status">
          <KeyValueTable entries={statusEntries} />
        </Section>
      )}

      {dataEntries.length > 0 && (
        <Section title="Data">
          <KeyValueTable entries={dataEntries} mask={isSecret} />
        </Section>
      )}
    </div>
  );
}

// ── Kind-specific overviews (plan A4 / C1) ──
//
// ponytail: only the few highest-value kinds get tailored sections; everything
// else uses GenericOverview. No per-kind framework — just small components.

function PodOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const status = obj.status ?? {};
  const statuses = status.containerStatuses ?? [];
  const totalRestarts = statuses.reduce(
    (sum, c) => sum + (c.restartCount ?? 0),
    0,
  );

  // Merge spec containers (image) with status containers (ready/restarts/state).
  const byName = new Map<string, ContainerStatus>();
  for (const c of statuses) if (c.name) byName.set(c.name, c);
  const rows = (spec.containers ?? []).map((c) => {
    const st = byName.get(c.name ?? "");
    return {
      name: c.name ?? "-",
      image: c.image ?? st?.image ?? "-",
      ready: st?.ready ?? false,
      restarts: st?.restartCount ?? 0,
      state: st?.state ? (Object.keys(st.state)[0] ?? "unknown") : "unknown",
    };
  });

  const summary: Array<[string, string]> = [];
  if (status.phase) summary.push(["phase", status.phase]);
  if (spec.nodeName) summary.push(["node", spec.nodeName]);
  if (status.podIP) summary.push(["podIP", status.podIP]);
  summary.push(["restarts", String(totalRestarts)]);

  return (
    <>
      <Section title="Pod">
        <KeyValueTable entries={summary} />
      </Section>
      <Section title="Containers">
        {rows.length === 0 ? (
          <p className="text-xs text-muted-foreground">None</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Image</TableHead>
                <TableHead className="text-center">Ready</TableHead>
                <TableHead className="text-center">Restarts</TableHead>
                <TableHead>State</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((c) => (
                <TableRow key={c.name}>
                  <TableCell className="font-mono text-xs">{c.name}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground break-all">
                    {c.image}
                  </TableCell>
                  <TableCell className="text-xs text-center">
                    {c.ready ? "Yes" : "No"}
                  </TableCell>
                  <TableCell className="text-xs tabular-nums text-center">
                    {c.restarts}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {c.state}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
    </>
  );
}

function ServiceOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const summary: Array<[string, string]> = [];
  if (spec.type) summary.push(["type", spec.type]);
  if (spec.clusterIP) summary.push(["clusterIP", spec.clusterIP]);
  const selector = Object.entries(spec.selector ?? {}) as Array<
    [string, string]
  >;
  const ports = spec.ports ?? [];

  return (
    <>
      <Section title="Service">
        <KeyValueTable entries={summary} />
      </Section>
      {ports.length > 0 && (
        <Section title="Ports">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Protocol</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ports.map((p, i) => (
                <TableRow key={p.name || i}>
                  <TableCell className="text-xs">{p.name || "-"}</TableCell>
                  <TableCell className="text-xs tabular-nums">
                    {p.port ?? "-"}
                  </TableCell>
                  <TableCell className="text-xs tabular-nums">
                    {String(p.targetPort ?? "-")}
                  </TableCell>
                  <TableCell className="text-xs">{p.protocol || "-"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}
      <Section title="Selector">
        <KeyValueTable entries={selector} />
      </Section>
    </>
  );
}

function ConfigMapOverview({ obj }: { obj: K8sObject }) {
  const keys = Object.keys(obj.data ?? {});
  return (
    <Section title="Keys">
      {keys.length === 0 ? (
        <p className="text-xs text-muted-foreground">No keys.</p>
      ) : (
        <ul className="space-y-1">
          {keys.map((k) => (
            <li key={k} className="font-mono text-xs text-foreground">
              {k}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function IngressOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const summary: Array<[string, string]> = [];
  if (spec.ingressClassName) summary.push(["class", spec.ingressClassName]);
  const hosts = (spec.rules ?? [])
    .map((r) => r.host)
    .filter(Boolean) as string[];
  const tlsHosts = (spec.tls ?? []).flatMap((t) => t.hosts ?? []);

  return (
    <>
      <Section title="Ingress">
        <KeyValueTable entries={summary} />
      </Section>
      <Section title="Hosts">
        {hosts.length === 0 ? (
          <p className="text-xs text-muted-foreground">None</p>
        ) : (
          <ul className="space-y-1">
            {hosts.map((h) => (
              <li key={h} className="font-mono text-xs text-foreground">
                {h}
              </li>
            ))}
          </ul>
        )}
      </Section>
      {tlsHosts.length > 0 && (
        <Section title="TLS">
          <ul className="space-y-1">
            {tlsHosts.map((h) => (
              <li key={h} className="font-mono text-xs text-foreground">
                {h}
              </li>
            ))}
          </ul>
        </Section>
      )}
    </>
  );
}

function PVCOverview({ obj }: { obj: K8sObject }) {
  const spec = obj.spec ?? {};
  const status = obj.status ?? {};
  const summary: Array<[string, string]> = [];
  if (status.phase) summary.push(["status", status.phase]);
  const capacity =
    status.capacity?.storage ?? spec.resources?.requests?.storage;
  if (capacity) summary.push(["capacity", capacity]);
  if (spec.storageClassName)
    summary.push(["storageClass", spec.storageClassName]);
  if (spec.volumeName) summary.push(["volume", spec.volumeName]);

  return (
    <Section title="PersistentVolumeClaim">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

// ── Kind-specific overviews for workload/batch/autoscaling kinds (Rancher
// parity). These previously fell to the bare GenericOverview. They read the
// varied spec/status shapes through a loose record view rather than widening
// the shared K8sObject type with a dozen one-off fields. ──

function rel(v: unknown): string | null {
  return typeof v === "string" && v ? formatRelativeTime(v) : null;
}

function WorkloadOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  // Defensive across Deployment/StatefulSet (spec.replicas + status.*Replicas)
  // and DaemonSet (status.desiredNumberScheduled / numberReady).
  const ready = status.readyReplicas ?? status.numberReady;
  const desired =
    spec.replicas ?? status.desiredNumberScheduled ?? status.replicas;
  const summary: Array<[string, string]> = [
    ["ready", `${displayNumber(ready, "0")}/${displayNumber(desired, "0")}`],
  ];
  const updated = status.updatedReplicas ?? status.updatedNumberScheduled;
  const available = status.availableReplicas ?? status.numberAvailable;
  if (updated != null) summary.push(["updated", displayNumber(updated)]);
  if (available != null) summary.push(["available", displayNumber(available)]);
  const strategy =
    asRecord(spec.strategy).type ?? asRecord(spec.updateStrategy).type;
  if (strategy) summary.push(["strategy", String(strategy)]);

  const selector = Object.entries(
    asRecord(asRecord(spec.selector).matchLabels),
  ) as Array<[string, string]>;
  const templateSpec = asRecord(asRecord(spec.template).spec);
  const containers = Array.isArray(templateSpec.containers)
    ? (templateSpec.containers as Array<Record<string, unknown>>)
    : [];
  const images = containers.map((c) => String(c.image ?? "")).filter(Boolean);

  return (
    <>
      <Section title="Workload">
        <KeyValueTable entries={summary} />
      </Section>
      {images.length > 0 && (
        <Section title="Images">
          <ul className="space-y-1">
            {images.map((img) => (
              <li
                key={img}
                className="font-mono text-xs text-foreground break-all"
              >
                {img}
              </li>
            ))}
          </ul>
        </Section>
      )}
      <Section title="Selector">
        <KeyValueTable entries={selector} />
      </Section>
    </>
  );
}

function ReplicaSetOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const selector = Object.entries(
    asRecord(asRecord(spec.selector).matchLabels),
  ) as Array<[string, string]>;
  const ready = displayNumber(status.readyReplicas, "0");
  const desired = displayNumber(spec.replicas ?? status.replicas, "0");
  const summary: Array<[string, string]> = [
    ["ready", `${ready}/${desired}`],
    ["available", displayNumber(status.availableReplicas, "0")],
  ];
  return (
    <>
      <Section title="ReplicaSet">
        <KeyValueTable entries={summary} />
      </Section>
      <Section title="Selector">
        <KeyValueTable entries={selector} />
      </Section>
    </>
  );
}

function JobOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [
    [
      "completions",
      `${displayNumber(status.succeeded, "0")}/${displayNumber(spec.completions, "1")}`,
    ],
  ];
  if (spec.parallelism != null)
    summary.push(["parallelism", displayNumber(spec.parallelism)]);
  if (spec.backoffLimit != null)
    summary.push(["backoffLimit", displayNumber(spec.backoffLimit)]);
  if (status.active != null) summary.push(["active", displayNumber(status.active)]);
  if (status.failed != null) summary.push(["failed", displayNumber(status.failed)]);
  if (spec.suspend != null)
    summary.push(["suspended", spec.suspend ? "Yes" : "No"]);
  const started = rel(status.startTime);
  const completed = rel(status.completionTime);
  if (started) summary.push(["started", started]);
  if (completed) summary.push(["completed", completed]);
  return (
    <Section title="Job">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

function CronJobOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [];
  if (spec.schedule) summary.push(["schedule", String(spec.schedule)]);
  if (spec.timeZone) summary.push(["timeZone", String(spec.timeZone)]);
  summary.push(["suspended", spec.suspend ? "Yes" : "No"]);
  if (spec.concurrencyPolicy)
    summary.push(["concurrency", String(spec.concurrencyPolicy)]);
  const active = Array.isArray(status.active) ? status.active.length : 0;
  summary.push(["active jobs", String(active)]);
  const lastSchedule = rel(status.lastScheduleTime);
  const lastSuccess = rel(status.lastSuccessfulTime);
  if (lastSchedule) summary.push(["last scheduled", lastSchedule]);
  if (lastSuccess) summary.push(["last successful", lastSuccess]);
  return (
    <Section title="CronJob">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

function HPAOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const target = asRecord(spec.scaleTargetRef);
  const summary: Array<[string, string]> = [];
  if (target.kind || target.name)
    summary.push([
      "target",
      `${displayNumber(target.kind, "")} ${displayNumber(target.name, "")}`.trim(),
    ]);
  summary.push([
    "min / max",
    `${displayNumber(spec.minReplicas, "1")} / ${displayNumber(spec.maxReplicas)}`,
  ]);
  summary.push([
    "replicas",
    `${displayNumber(status.currentReplicas, "0")} → ${displayNumber(status.desiredReplicas, "0")}`,
  ]);
  const lastScale = rel(status.lastScaleTime);
  if (lastScale) summary.push(["last scaled", lastScale]);

  // Resource metrics: pair each spec target with its live current value from
  // status.currentMetrics so the row reads "current X% / target Y%".
  const current = new Map<string, string>();
  (Array.isArray(status.currentMetrics) ? status.currentMetrics : []).forEach(
    (m) => {
      const r = asRecord(asRecord(m).resource);
      const c = asRecord(r.current);
      const name = displayNumber(r.name, "");
      const val =
        c.averageUtilization != null
          ? `${c.averageUtilization}%`
          : displayNumber(c.averageValue ?? c.value, "");
      if (name && val) current.set(name, val);
    },
  );
  const metrics = (Array.isArray(spec.metrics) ? spec.metrics : [])
    .map((m) => {
      const r = asRecord(asRecord(m).resource);
      const t = asRecord(r.target);
      const name = displayNumber(r.name, "");
      const target =
        t.averageUtilization != null
          ? `${t.averageUtilization}%`
          : displayNumber(t.averageValue ?? t.value, "");
      if (!name || !target) return null;
      const cur = current.get(name);
      return [name, cur ? `${cur} / target ${target}` : `target ${target}`] as [
        string,
        string,
      ];
    })
    .filter(Boolean) as Array<[string, string]>;

  return (
    <>
      <Section title="HorizontalPodAutoscaler">
        <KeyValueTable entries={summary} />
      </Section>
      {metrics.length > 0 && (
        <Section title="Metrics">
          <KeyValueTable entries={metrics} />
        </Section>
      )}
    </>
  );
}
