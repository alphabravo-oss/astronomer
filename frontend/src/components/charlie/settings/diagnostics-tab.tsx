import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Bot,
  Database,
  KeyRound,
  Loader2,
  Network,
  RefreshCw,
  Shield,
  Sparkles,
} from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { queryKeys } from "@/lib/query-keys";
import { formatRelativeTime } from "@/lib/utils";
import { completeDiagnostics } from "@/components/charlie/admin-utils";
import { getCharlieDiagnostics } from "@/lib/api/charlie-admin";
import { Section, Unavailable, button } from "./shared";

const diagnosticIcon: Record<string, typeof Database> = {
  local_config: Database,
  product_bridge_mtls: Network,
  agent_primary: Bot,
  agent_standby: Bot,
  central_via_agent: Network,
  leader_epoch: Activity,
  route_rag: Sparkles,
  mcp_tls_discovery: Shield,
  oci_artifacts: Database,
  credential_expiry: KeyRound,
};
export function DiagnosticsTab() {
  const q = useQuery({
    queryKey: queryKeys.charlie.adminDiagnostics,
    queryFn: getCharlieDiagnostics,
    retry: false,
    refetchInterval: 30000,
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Running Charlie diagnostics"
      />
    );
  if (q.isError)
    return <Unavailable name="Diagnostics" retry={() => void q.refetch()} />;
  const checks = completeDiagnostics(q.data?.checks ?? []);
  return (
    <Section
      title="Independent diagnostic checks"
      description="Charlie health never participates in Astronomer's core readiness. Failures degrade only Charlie features."
    >
      {q.data?.overall === "inactive" && (
        <p className="rounded-lg border border-border bg-muted/20 p-3 text-sm text-muted-foreground">
          Charlie authority is disabled, so connectivity checks are skipped. Raise
          mode on the Mode tab to probe the agent and Charlie.
        </p>
      )}
      <div className="flex items-center justify-between">
        <StatusBadge
          status={q.data?.overall ?? "unknown"}
          label={`Overall: ${q.data?.overall ?? "unknown"}`}
        />
        <button onClick={() => void q.refetch()} className={button}>
          <RefreshCw className="h-4 w-4" />
          Run again
        </button>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        {checks.map((check) => {
          const Icon = diagnosticIcon[check.id] ?? Activity;
          return (
            <article
              key={check.id}
              className="rounded-lg border border-border p-4"
            >
              <div className="flex items-center gap-2">
                <Icon className="h-4 w-4" />
                <h3 className="flex-1 text-sm font-medium">{check.label}</h3>
                <StatusBadge status={check.state} />
              </div>
              <p className="mt-2 text-xs text-muted-foreground">
                {check.summary}
              </p>
              {check.expiresAt && (
                <p className="mt-1 text-xs text-muted-foreground">
                  Expires {formatRelativeTime(check.expiresAt)}
                </p>
              )}
              {check.nextAction && (
                <p className="mt-2 rounded-md bg-muted p-2 text-xs">
                  Next action: {check.nextAction}
                </p>
              )}
            </article>
          );
        })}
      </div>
      {q.data?.correlationId && (
        <p className="text-xs text-muted-foreground">
          Correlation: <span className="font-mono">{q.data.correlationId}</span>
        </p>
      )}
    </Section>
  );
}
