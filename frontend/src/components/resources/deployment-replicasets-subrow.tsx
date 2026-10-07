import { StatusBadge } from "@/components/ui/status-badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { AgeCell } from "@/components/ui/age-cell";
import { ownedReplicaSets } from "@/components/resources/deployment-replicasets";
import { useGenericResources } from "@/lib/hooks/kubernetes-proxy";

const REPLICASET_LIMIT = 500;

/**
 * Expanded deployment row: the ReplicaSets it owns, newest revision first, so
 * a stuck rollout (old set still holding replicas) is visible without leaving
 * the list. Loads lazily, only for rows the operator expands.
 */
export function DeploymentReplicaSetsSubRow({
  clusterId,
  namespace,
  name,
}: {
  clusterId: string;
  namespace: string;
  name: string;
}) {
  const query = useGenericResources(clusterId, "replicasets", {
    namespace,
    limit: REPLICASET_LIMIT,
  });
  if (query.isLoading) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        Loading ReplicaSets…
      </p>
    );
  }
  if (query.isError) {
    return (
      <p role="alert" className="text-sm text-status-error">
        ReplicaSets could not be loaded for {name}.
      </p>
    );
  }
  const sets = ownedReplicaSets(query.data?.data ?? [], name);
  if (sets.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No ReplicaSets found for this deployment.
      </p>
    );
  }
  return (
    <Table layout="scroll" aria-label={`ReplicaSets of ${name}`}>
      <TableHeader>
        <TableRow>
          <TableHead>ReplicaSet</TableHead>
          <TableHead className="text-right">Revision</TableHead>
          <TableHead className="text-right">Ready / desired</TableHead>
          <TableHead className="text-right">Available</TableHead>
          <TableHead>Age</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sets.map((rs) => (
          <TableRow key={rs.name}>
            <TableCell className="font-mono text-xs">
              {rs.name}
              {rs.current ? (
                <StatusBadge status="Current" className="ml-2" />
              ) : null}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {rs.revision ?? "—"}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {rs.ready} / {rs.desired}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {rs.available}
            </TableCell>
            <TableCell>
              <AgeCell value={rs.createdAt} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** DataTable `renderSubRow` for the deployments list; undefined for other kinds. */
export function replicaSetsSubRow(clusterId: string, resourceType: string) {
  if (resourceType !== "deployments") return undefined;
  return (row: { namespace: string; name: string }) => (
    <DeploymentReplicaSetsSubRow
      clusterId={clusterId}
      namespace={row.namespace}
      name={row.name}
    />
  );
}
