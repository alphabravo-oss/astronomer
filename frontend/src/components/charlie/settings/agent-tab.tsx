import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { queryKeys } from "@/lib/query-keys";
import { formatRelativeTime } from "@/lib/utils";
import { getCharlieAgent } from "@/lib/api/charlie-admin";
import { Meta, Section, Unavailable } from "./shared";

export function AgentTab() {
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAgent,
    queryFn: getCharlieAgent,
    retry: false,
    refetchInterval: 15000,
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie agent"
      />
    );
  if (q.isError || !q.data)
    return <Unavailable name="Agent status" retry={() => void q.refetch()} />;
  const a = q.data;
  const workloadReady =
    a.desiredReplicas > 0 && a.readyReplicas >= a.desiredReplicas;
  const authorityIdle =
    a.applicationState === "inactive" || a.applicationState === "disabled";
  return (
    <Section
      title="Charlie product agent"
      description="Replica readiness is the local Charlie agent StatefulSet. Leader and heartbeat fields appear after the agent enrolls with Charlie."
    >
      <div aria-live="polite">
        <StatusBadge
          status={workloadReady ? "ready" : a.applicationState}
          label={
            workloadReady && authorityIdle
              ? "Workload ready · Charlie disabled"
              : `Agent state: ${a.applicationState.replaceAll("_", " ")}`
          }
        />
      </div>
      {workloadReady && authorityIdle && (
        <p className="rounded-lg border border-border bg-muted/20 p-3 text-sm text-muted-foreground">
          Both replicas are running. Charlie authority is disabled, so the agent
          stays idle until you raise mode.
        </p>
      )}
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Meta label="Workload state" value={a.applicationState} />
        <Meta
          label="Replicas"
          value={`${a.readyReplicas}/${a.desiredReplicas} ready`}
        />
        <Meta label="Leader" value={a.leaderReplica} />
        <Meta label="Standby" value={a.standbyReplicas?.join(", ")} />
        <Meta label="Fencing epoch" value={a.fencingEpoch} />
        <Meta
          label="Last heartbeat"
          value={
            a.lastHeartbeatAt
              ? formatRelativeTime(a.lastHeartbeatAt)
              : undefined
          }
        />
        <Meta label="Agent version" value={a.agentVersion} />
        <Meta label="Chart version" value={a.chartVersion} />
        <Meta label="Chart digest" value={a.chartDigest} />
        <Meta label="Image digest" value={a.imageDigest} />
      </dl>
      <div className="overflow-x-auto rounded-lg border border-border">
        <Table className="w-full min-w-[44rem] text-left text-sm">
          <caption className="sr-only">
            Product-observed Charlie agent replica status
          </caption>
          <TableHeader className="border-b border-border text-xs text-muted-foreground">
            <TableRow>
              <TableHead scope="col" className="p-3">Ordinal</TableHead>
              <TableHead scope="col" className="p-3">Instance</TableHead>
              <TableHead scope="col" className="p-3">Role</TableHead>
              <TableHead scope="col" className="p-3">State</TableHead>
              <TableHead scope="col" className="p-3">Last heartbeat</TableHead>
              <TableHead scope="col" className="p-3">Version</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className="divide-y divide-border">
            {a.replicas.length ? a.replicas.map((replica) => (
              <TableRow key={replica.ordinal}>
                <TableCell className="p-3">{replica.ordinal}</TableCell>
                <TableCell className="break-all p-3 font-mono text-xs">
                  {replica.instanceId || "Not reported"}
                </TableCell>
                <TableCell className="p-3">{replica.role}</TableCell>
                <TableCell className="p-3">
                  <StatusBadge status={replica.state} />
                </TableCell>
                <TableCell className="p-3">
                  {replica.lastHeartbeatAt
                    ? formatRelativeTime(replica.lastHeartbeatAt)
                    : "Not reported"}
                </TableCell>
                <TableCell className="p-3">{replica.version || "Not reported"}</TableCell>
              </TableRow>
            )) : (
              <TableRow>
                <TableCell colSpan={6} className="p-4 text-center text-muted-foreground">
                  No product-observed replica status is available.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <div className="rounded-lg border border-border bg-muted/20 p-4 text-sm text-muted-foreground">
        The generic Charlie agent is installed from Charlie when you connect, and
        removed when you disconnect. Charlie publishes a new digest and Astronomer
        upgrades the same StatefulSet.
      </div>
    </Section>
  );
}
