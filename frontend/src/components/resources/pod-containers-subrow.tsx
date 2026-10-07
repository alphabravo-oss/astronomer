import { StatusBadge } from "@/components/ui/status-badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { formatRelativeTime } from "@/lib/utils";
import type { Container, Pod } from "@/types";

/** Last termination summary, e.g. `OOMKilled, exit 137, 2 hours ago`. */
export function lastTerminationSummary(container: Container): string {
  const terminated = container.lastState?.terminated;
  if (!terminated) return "None";
  const parts = [terminated.reason ?? "Terminated"];
  if (terminated.exitCode !== undefined) {
    parts.push(`exit ${terminated.exitCode}`);
  }
  if (terminated.finishedAt) {
    parts.push(formatRelativeTime(terminated.finishedAt));
  }
  return parts.join(", ");
}

/**
 * Expanded pod row: each container's state, restarts and last termination.
 * Reads only the container data the pods list already returned.
 */
export function PodContainersSubRow({ pod }: { pod: Pod }) {
  if (pod.containers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No container details reported for this pod.
      </p>
    );
  }
  return (
    <Table layout="scroll" aria-label={`Containers of ${pod.name}`}>
      <TableHeader>
        <TableRow>
          <TableHead>Container</TableHead>
          <TableHead>State</TableHead>
          <TableHead className="text-right">Restarts</TableHead>
          <TableHead>Last termination</TableHead>
          <TableHead>Image</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {pod.containers.map((container) => (
          <TableRow key={`${container.init ? "init:" : ""}${container.name}`}>
            <TableCell className="font-mono text-xs">
              {container.name}
              {container.init ? (
                <span className="ml-1 text-muted-foreground">(init)</span>
              ) : null}
            </TableCell>
            <TableCell>
              <StatusBadge
                status={container.reason ?? container.status}
                size="sm"
              />
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {container.restartCount}
            </TableCell>
            <TableCell className="text-xs">
              {lastTerminationSummary(container)}
            </TableCell>
            <TableCell className="break-all font-mono text-xs text-muted-foreground">
              {container.image}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
