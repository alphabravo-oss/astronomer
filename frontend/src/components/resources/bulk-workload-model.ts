import {
  getWorkloadOperation,
  restartWorkload,
  scaleWorkload,
} from "@/lib/api/workloads";
import {
  OperationFailedError,
  OperationPartialError,
  pollOperation,
  type OperationSnapshot,
} from "@/lib/api/operation-polling";

export interface BulkWorkloadTarget {
  kind: string;
  namespace: string;
  name: string;
}
export type BulkWorkloadAction = "restart" | "scale";
export interface BulkWorkloadResult {
  target: BulkWorkloadTarget;
  status: "succeeded" | "failed" | "unconfirmed" | "not-started";
  detail?: string;
  operationId?: string;
}
export const MAX_BULK_WORKLOADS = 50;

export function supportsBulkWorkload(
  target: BulkWorkloadTarget,
  action: BulkWorkloadAction,
): boolean {
  return (
    action === "scale"
      ? ["deployment", "statefulset"]
      : ["deployment", "statefulset", "daemonset"]
  ).includes(target.kind.toLowerCase());
}

/** Never start the next write until the preceding durable operation is terminal. */
export async function runBulkWorkloads({
  clusterId,
  targets,
  action,
  replicas,
  signal,
  allowed,
  onProgress,
}: {
  clusterId: string;
  targets: readonly BulkWorkloadTarget[];
  action: BulkWorkloadAction;
  replicas: number;
  signal: AbortSignal;
  allowed: (target: BulkWorkloadTarget) => boolean;
  onProgress?: (results: BulkWorkloadResult[]) => void;
}): Promise<BulkWorkloadResult[]> {
  if (!targets.length || targets.length > MAX_BULK_WORKLOADS)
    throw new Error(`Select 1–${MAX_BULK_WORKLOADS} workloads`);
  if (action === "scale" && (!Number.isSafeInteger(replicas) || replicas < 0))
    throw new Error("Replicas must be a non-negative integer");
  const results: BulkWorkloadResult[] = [];
  let stopped = false;
  for (const target of targets) {
    let receipt: OperationSnapshot | undefined;
    if (signal.aborted || stopped) {
      results.push({
        target,
        status: "not-started",
        detail: "Queue stopped; no request sent.",
      });
    } else if (!supportsBulkWorkload(target, action) || !allowed(target)) {
      results.push({
        target,
        status: "not-started",
        detail: "Unsupported workload or permission no longer available.",
      });
    } else {
      try {
        receipt =
          action === "scale"
            ? await scaleWorkload(
                clusterId,
                target.kind,
                target.namespace,
                target.name,
                replicas,
                { signal },
              )
            : await restartWorkload(
                clusterId,
                target.kind,
                target.namespace,
                target.name,
                { signal },
              );
        await pollOperation(receipt, getWorkloadOperation, { signal });
        results.push({ target, status: "succeeded", operationId: receipt.id });
      } catch (error) {
        const terminal =
          error instanceof OperationFailedError ||
          error instanceof OperationPartialError;
        results.push({
          target,
          status: terminal ? "failed" : "unconfirmed",
          operationId: receipt?.id,
          detail: error instanceof Error ? error.message : "Operation failed",
        });
        // A lost response, cancellation or poll failure is not evidence that a
        // queued operation stopped. Do not compound it with subsequent writes.
        if (!terminal) stopped = true;
      }
    }
    onProgress?.([...results]);
  }
  return results;
}
