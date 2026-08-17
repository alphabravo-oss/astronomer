import { useQuery } from "@tanstack/react-query";
import { GitBranch, Loader2 } from "lucide-react";
import { Link } from "@/lib/link";
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
import { Meta, Section, Unavailable, button } from "./shared";

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
  return (
    <Section
      title="Charlie product agent"
      description="Read-only product-agent status reported by the management plane. Charlie central remains a separate service."
    >
      <div aria-live="polite">
        <StatusBadge
          status={a.applicationState}
          label={`Agent state: ${a.applicationState.replaceAll("_", " ")}`}
        />
      </div>
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Meta label="Delivery assignment" value={a.applicationState} />
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
      <div className="rounded-lg border border-border bg-muted/20 p-4 text-sm">
        <p className="font-medium text-foreground">Lifecycle is managed by Flux delivery</p>
        <p className="mt-1 text-muted-foreground">
          Install, upgrade, rollback, credential rotation, and removal are declared through
          versioned delivery bundles and targets, with preview and rollout controls.
        </p>
        <Link href="/dashboard/delivery" className={`${button} mt-3 inline-flex`}>
          <GitBranch className="h-4 w-4" />
          Open Continuous Delivery
        </Link>
      </div>
    </Section>
  );
}
