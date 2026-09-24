import { queryKeys } from "@/lib/query-keys";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { getAlertEvent, getAlertRule } from "@/lib/api/alerting";
import { QueryStates } from "@/components/ui/query-states";
import { ModalShell } from "@/components/ui/modal-shell";
import { StatusBadge } from "@/components/ui/status-badge";
import { useInvestigationParam } from "@/components/resources/resource-navigation-context";
import { usePermissionDecision } from "@/lib/permission-hooks";

export function AlertInvestigation({
  id,
  onClose,
}: {
  id: string;
  onClose: () => void;
}) {
  const query = useQuery({
    queryKey: queryKeys.alerting.event(id),
    queryFn: () => getAlertEvent(id),
    throwOnError: false,
  });
  return (
    <ModalShell title="Alert investigation" onClose={onClose} size="lg">
      <QueryStates
        query={query}
        permission="alerts:read"
        errorTitle="Alert unavailable"
      >
        {(event) => (
          <div className="space-y-4">
            <StatusBadge status={event.status} />
            <p className="whitespace-pre-wrap break-words">{event.message}</p>
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt>Rule</dt>
              <dd>{event.ruleName || event.ruleId || "Unavailable"}</dd>
              <dt>Fired</dt>
              <dd>{event.firedAt}</dd>
              <dt>Acknowledged</dt>
              <dd>{event.acknowledgedAt || "—"}</dd>
              <dt>Resolved</dt>
              <dd>{event.resolvedAt || "—"}</dd>
              <dt>Namespace</dt>
              <dd>{event.namespace || "—"}</dd>
            </dl>
            {event.clusterId && (
              <AlertClusterLinks
                clusterId={event.clusterId}
                ruleId={event.ruleId}
                clusterName={event.clusterName}
              />
            )}
          </div>
        )}
      </QueryStates>
    </ModalShell>
  );
}
function AlertClusterLinks({
  clusterId,
  clusterName,
  ruleId,
}: {
  clusterId: string;
  clusterName: string | null;
  ruleId: string;
}) {
  const read = usePermissionDecision("clusters", "read", {
    type: "cluster",
    id: clusterId,
  });
  const metrics = usePermissionDecision("monitoring", "read", {
    type: "cluster",
    id: clusterId,
  });
  const alerts = usePermissionDecision("alerts", "read", {
    type: "cluster",
    id: clusterId,
  });
  return (
    <div className="flex flex-wrap gap-3 text-sm text-primary">
      {read.allowed && (
        <Link to={String(`/dashboard/clusters/${clusterId}`)}>
          Cluster: {clusterName || clusterId}
        </Link>
      )}
      {metrics.allowed && (
        <Link to={String(`/dashboard/clusters/${clusterId}/metrics`)}>
          Cluster metrics
        </Link>
      )}
      {alerts.allowed && ruleId && (
        <Link
          to={String(
            `/dashboard/clusters/${clusterId}/alerting?tab=rules&rule=${encodeURIComponent(ruleId)}`,
          )}
        >
          Investigate rule
        </Link>
      )}
    </div>
  );
}
export function AlertRuleInspection() {
  const [id, setId] = useInvestigationParam("rule");
  const query = useQuery({
    queryKey: queryKeys.alerting.rule(id),
    queryFn: () => getAlertRule(id),
    enabled: !!id,
    throwOnError: false,
  });
  if (!id) return null;
  return (
    <ModalShell title="Alert rule" onClose={() => setId("")} size="lg">
      <QueryStates
        query={query}
        permission="alerts:read"
        errorTitle="Rule unavailable"
      >
        {(rule) => (
          <div className="space-y-3">
            <p className="font-semibold">{rule.name}</p>
            <p>{rule.description}</p>
            <dl className="grid grid-cols-2 gap-2 text-sm">
              <dt>Type</dt>
              <dd>{rule.type}</dd>
              <dt>Severity</dt>
              <dd>{rule.severity}</dd>
              <dt>Enabled</dt>
              <dd>{rule.enabled ? "Yes" : "No"}</dd>
              <dt>Threshold</dt>
              <dd>{rule.threshold ?? "Not applicable"}</dd>
              <dt>Duration</dt>
              <dd>{rule.duration || "Immediate"}</dd>
            </dl>
            <p className="text-sm">Query</p>
            <pre className="whitespace-pre-wrap break-all text-xs">
              {rule.query || "No query configured"}
            </pre>
            <details>
              <summary>Advanced rule configuration</summary>
              <pre className="whitespace-pre-wrap break-all text-xs">
                {JSON.stringify(rule, null, 2)}
              </pre>
            </details>
          </div>
        )}
      </QueryStates>
    </ModalShell>
  );
}
