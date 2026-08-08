import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, RefreshCw, Trash2 } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { formatRelativeTime } from "@/lib/utils";
import {
  getCharlieAgent,
  getCharlieConnection,
  runCharlieAgentAction,
  uninstallCharlieAgent,
} from "@/lib/api/charlie-admin";
import { Meta, Section, Unavailable, button, primary } from "./shared";

export function agentActionsForState(
  state: string,
): Array<"install" | "upgrade" | "rollback" | "rotate"> {
  if (["not_installed", "inactive", "disconnected"].includes(state))
    return ["install"];
  if (["ready", "degraded"].includes(state))
    return ["upgrade", "rollback", "rotate"];
  return [];
}

export function AgentTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAgent,
    queryFn: getCharlieAgent,
    retry: false,
    refetchInterval: 15000,
  });
  const connection = useQuery({
    queryKey: queryKeys.charlie.adminConnection,
    queryFn: getCharlieConnection,
    retry: false,
    refetchInterval: 15000,
  });
  const [confirm, setConfirm] = useState(false);
  const action = useMutation({
    mutationFn: (a: "install" | "upgrade" | "rollback" | "rotate") =>
      runCharlieAgentAction(a),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      toastSuccess("Charlie agent lifecycle request accepted");
    },
    onError: (e) => toastApiError("Agent action failed", e),
  });
  const uninstall = useMutation({
    mutationFn: uninstallCharlieAgent,
    onSuccess: () => {
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      toastSuccess("Charlie agent uninstall requested");
    },
    onError: (e) => toastApiError("Uninstall failed", e),
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
  const trustReady = Boolean(
    connection.data?.connected &&
      connection.data.signingFingerprint &&
      connection.data.packageDigest,
  );
  const actions = agentActionsForState(a.applicationState);
  return (
    <Section
      title="Charlie product agent"
      description="Astronomer manages the product-side agent; Charlie central remains a separate service."
    >
      <div aria-live="polite">
        <StatusBadge
          status={a.applicationState}
          label={`Agent state: ${a.applicationState.replaceAll("_", " ")}`}
        />
      </div>
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Meta label="Argo application" value={a.applicationState} />
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
      <div className="flex flex-wrap gap-2">
        {actions.map((v) => (
          <button
            key={v}
            disabled={action.isPending || (v === "install" && !trustReady)}
            onClick={() => action.mutate(v)}
            className={button}
          >
            <RefreshCw className="h-4 w-4" />
            {v[0].toUpperCase() + v.slice(1)}
          </button>
        ))}
        <button
          disabled={action.isPending || uninstall.isPending || a.applicationState === "not_installed"}
          onClick={() => setConfirm(true)}
          className={`${button} text-status-error`}
        >
          <Trash2 className="h-4 w-4" />
          Uninstall
        </button>
      </div>
      {actions.includes("install") && !trustReady && (
        <p role="status" className="text-sm text-status-warning">
          Install is unavailable until a signed Charlie onboarding package has
          been validated and consumed into an active connection with recorded
          signing and package digests.
        </p>
      )}
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => uninstall.mutate()}
        title="Uninstall Charlie agent"
        description="This removes the Astronomer-side Charlie agent. It does not delete Charlie central data or Astronomer audit history."
        confirmText="Uninstall"
        confirmValue="UNINSTALL CHARLIE"
        variant="destructive"
        loading={uninstall.isPending}
      />
    </Section>
  );
}

