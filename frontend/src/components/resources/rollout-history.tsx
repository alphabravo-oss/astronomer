import { useMemo } from "react";
import { Link } from "@/lib/link";
import { useK8sResource } from "@/lib/hooks";
import { detailHref, k8sListPath } from "@/lib/k8s-paths";
import { formatRelativeTime } from "@/lib/utils";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Loader2 } from "lucide-react";

interface RolloutOwner {
  kind: string;
  name: string;
  uid?: string;
}

interface OwnerReference {
  kind?: string;
  name?: string;
  uid?: string;
}

interface RevisionResource {
  metadata?: {
    name?: string;
    uid?: string;
    creationTimestamp?: string;
    ownerReferences?: OwnerReference[];
    annotations?: Record<string, string>;
  };
  revision?: number;
  spec?: { replicas?: number };
  status?: {
    replicas?: number;
    readyReplicas?: number;
    availableReplicas?: number;
    active?: number;
    succeeded?: number;
    failed?: number;
  };
}

interface RevisionList {
  items?: RevisionResource[];
}

export interface RolloutHistoryEntry {
  name: string;
  revision?: number;
  createdAt?: string;
  changeCause?: string;
  status: string;
  resourceType?: "replicasets" | "jobs";
}

interface RolloutSource {
  path: string;
  resourceType?: RolloutHistoryEntry["resourceType"];
}

export function rolloutSourceForKind(
  kind: string,
  namespace: string,
): RolloutSource | undefined {
  switch (kind) {
    case "Deployment":
      return {
        path: k8sListPath("replicasets", namespace),
        resourceType: "replicasets",
      };
    case "StatefulSet":
    case "DaemonSet":
      return {
        path: `apis/apps/v1/namespaces/${namespace}/controllerrevisions`,
      };
    case "CronJob":
      return {
        path: k8sListPath("jobs", namespace),
        resourceType: "jobs",
      };
    default:
      return undefined;
  }
}

export function supportsRolloutHistory(kind: string): boolean {
  return ["Deployment", "StatefulSet", "DaemonSet", "CronJob"].includes(
    kind,
  );
}

function belongsToOwner(
  resource: RevisionResource,
  owner: RolloutOwner,
): boolean {
  return (resource.metadata?.ownerReferences ?? []).some((reference) => {
    if (owner.uid && reference.uid) return reference.uid === owner.uid;
    return reference.kind === owner.kind && reference.name === owner.name;
  });
}

function numericRevision(resource: RevisionResource): number | undefined {
  if (typeof resource.revision === "number") return resource.revision;
  const raw =
    resource.metadata?.annotations?.["deployment.kubernetes.io/revision"];
  if (!raw) return undefined;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function rolloutStatus(resource: RevisionResource): string {
  const status = resource.status ?? {};
  if (status.readyReplicas !== undefined || status.replicas !== undefined) {
    return `${status.readyReplicas ?? 0}/${status.replicas ?? resource.spec?.replicas ?? 0} ready`;
  }
  const jobs = [
    status.active ? `${status.active} active` : "",
    status.succeeded ? `${status.succeeded} succeeded` : "",
    status.failed ? `${status.failed} failed` : "",
  ].filter(Boolean);
  return jobs.join(", ") || "Recorded";
}

/** Convert an untrusted Kubernetes list response into stable, owner-scoped rows. */
export function rolloutHistoryEntries(
  data: RevisionList | undefined,
  owner: RolloutOwner,
  resourceType?: RolloutHistoryEntry["resourceType"],
): RolloutHistoryEntry[] {
  return (data?.items ?? [])
    .filter((item) => belongsToOwner(item, owner))
    .map((item) => ({
      name: item.metadata?.name ?? "unknown",
      revision: numericRevision(item),
      createdAt: item.metadata?.creationTimestamp,
      changeCause:
        item.metadata?.annotations?.["kubernetes.io/change-cause"] ??
        item.metadata?.annotations?.["kubectl.kubernetes.io/change-cause"],
      status: rolloutStatus(item),
      resourceType,
    }))
    .sort((left, right) => {
      if (left.revision !== right.revision) {
        return (right.revision ?? -1) - (left.revision ?? -1);
      }
      return (right.createdAt ?? "").localeCompare(left.createdAt ?? "");
    });
}

export function RolloutHistory({
  clusterId,
  namespace,
  owner,
}: {
  clusterId: string;
  namespace: string;
  owner: RolloutOwner;
}) {
  const source = rolloutSourceForKind(owner.kind, namespace);
  const { data, isLoading, error } = useK8sResource(
    clusterId,
    source?.path ?? "",
    !!source,
  );
  const entries = useMemo(
    () =>
      rolloutHistoryEntries(
        data as RevisionList | undefined,
        owner,
        source?.resourceType,
      ),
    [data, owner, source?.resourceType],
  );

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-24">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (error) {
    return (
      <div className="py-24 text-center text-sm text-status-error">
        Failed to load rollout history: {(error as Error).message}
      </div>
    );
  }
  if (entries.length === 0) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No rollout revisions were reported for this resource.
      </p>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        History is derived from Kubernetes-owned revision objects. The newest
        retained revision appears first; retention is controlled by the
        workload and its controller.
      </p>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Revision</TableHead>
            <TableHead>Resource</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Change cause</TableHead>
            <TableHead>Created</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((entry, index) => (
            <TableRow key={entry.name}>
              <TableCell className="text-xs tabular-nums">
                {entry.revision ?? "-"}
                {index === 0 && (
                  <span className="ml-2 rounded bg-status-info/10 px-1.5 py-0.5 text-[10px] font-medium text-status-info">
                    Latest
                  </span>
                )}
              </TableCell>
              <TableCell className="font-mono text-xs">
                {entry.resourceType ? (
                  <Link
                    href={detailHref(
                      clusterId,
                      entry.resourceType,
                      namespace,
                      entry.name,
                    )}
                    className="text-foreground hover:underline"
                  >
                    {entry.name}
                  </Link>
                ) : (
                  entry.name
                )}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {entry.status}
              </TableCell>
              <TableCell className="max-w-xs text-xs text-muted-foreground">
                {entry.changeCause || "Not recorded"}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {entry.createdAt ? formatRelativeTime(entry.createdAt) : "-"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
