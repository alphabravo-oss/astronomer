import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ModalShell } from "@/components/ui/modal-shell";
import { Input } from "@/components/ui/input";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError } from "@/lib/toast";
import {
  runBulkWorkloads,
  supportsBulkWorkload,
  MAX_BULK_WORKLOADS,
  type BulkWorkloadTarget,
  type BulkWorkloadAction,
  type BulkWorkloadResult,
} from "./bulk-workload-model";

export interface ExplorerBulkWorkloads<T> {
  target: (row: T) => BulkWorkloadTarget;
}

export function BulkWorkloadActions<T>({
  clusterId,
  rows,
  config,
  allowed,
}: {
  clusterId: string;
  rows: T[];
  config: ExplorerBulkWorkloads<T>;
  allowed: (target: BulkWorkloadTarget, action: BulkWorkloadAction) => boolean;
}) {
  const client = useQueryClient();
  const [action, setAction] = useState<BulkWorkloadAction | null>(null);
  const [replicas, setReplicas] = useState("1");
  const [running, setRunning] = useState(false);
  const [results, setResults] = useState<BulkWorkloadResult[] | null>(null);
  const controller = useRef<AbortController | null>(null);
  const latestAllowed = useRef(allowed);
  useEffect(() => {
    latestAllowed.current = allowed;
  }, [allowed]);
  useEffect(() => () => controller.current?.abort(), []);
  const targets = rows.map(config.target);
  const canRun = (verb: BulkWorkloadAction) =>
    targets.length > 0 &&
    targets.length <= MAX_BULK_WORKLOADS &&
    targets.every(
      (target) => supportsBulkWorkload(target, verb) && allowed(target, verb),
    );
  const run = async () => {
    if (!action || !canRun(action) || running) return;
    const abort = new AbortController();
    controller.current = abort;
    setRunning(true);
    setResults([]);
    try {
      const completed = await runBulkWorkloads({
        clusterId,
        targets,
        action,
        replicas: Number(replicas),
        signal: abort.signal,
        allowed: (target) => latestAllowed.current(target, action),
        onProgress: setResults,
      });
      setResults(completed);
    } catch (error) {
      toastApiError("Could not start workload operations", error);
      setResults(null);
    } finally {
      setRunning(false);
      setAction(null);
      void client.invalidateQueries({ queryKey: queryKeys.workloads.all });
      void client.invalidateQueries({ queryKey: queryKeys.k8s.all });
    }
  };
  return (
    <>
      {(["restart", "scale"] as const).map((verb) => (
        <ActionButton
          key={verb}
          size="sm"
          disabled={running || !canRun(verb)}
          disabledReason={`Select at most ${MAX_BULK_WORKLOADS} supported workloads with ${verb} permission on every namespace.`}
          onClick={() => setAction(verb)}
        >
          {verb === "scale" ? "Scale selected" : "Restart selected"}
        </ActionButton>
      ))}
      <ConfirmDialog
        open={!!action && !running && results === null}
        onClose={() => setAction(null)}
        title={`${action === "scale" ? "Scale" : "Restart"} ${targets.length} workloads`}
        description="Runs one durable operation at a time. Every target is authorized and audited independently. An unconfirmed result stops subsequent writes."
        confirmValue={`${action} ${targets.length}`}
        confirmText="Run operations"
        onConfirm={() => void run()}
        confirmDisabledReason={
          action === "scale" &&
          (!replicas.trim() ||
            !Number.isSafeInteger(Number(replicas)) ||
            Number(replicas) < 0)
            ? "Enter a non-negative integer replica count"
            : undefined
        }
      >
        {action === "scale" && (
          <label className="block space-y-2">
            Replicas for every target
            <Input
              type="number"
              min={0}
              step={1}
              value={replicas}
              onChange={(event) => setReplicas(event.target.value)}
            />
          </label>
        )}
        <p className="my-2 text-sm text-status-warning">
          Scaling to zero stops serving traffic. Restarts may temporarily reduce
          capacity. Flux may restore declared replicas.
        </p>
        <ul className="max-h-48 overflow-auto font-mono text-xs">
          {targets.map((target) => (
            <li key={`${target.namespace}/${target.name}`}>
              {target.kind} {target.namespace}/{target.name}
            </li>
          ))}
        </ul>
      </ConfirmDialog>
      {results !== null && (
        <ModalShell
          title={
            running
              ? "Workload operations running"
              : "Workload operation results"
          }
          onClose={() => {
            controller.current?.abort();
            if (!running) setResults(null);
          }}
          footer={
            <ActionButton
              onClick={() => {
                controller.current?.abort();
                if (!running) setResults(null);
              }}
            >
              {running ? "Stop remaining operations" : "Done"}
            </ActionButton>
          }
        >
          <p className="text-sm text-muted-foreground">
            Stopping cancels polling and unsent requests, not an operation
            already accepted by the server. Check unconfirmed operation IDs
            before retrying.
          </p>
          <ul
            aria-live="polite"
            className="mt-3 max-h-80 space-y-3 overflow-auto"
          >
            {results.map((result) => (
              <li
                key={`${result.target.namespace}/${result.target.name}`}
                className="text-sm"
              >
                <span className="font-mono">
                  {result.target.namespace}/{result.target.name}
                </span>
                : {result.status}
                {result.detail && <p>{result.detail}</p>}
                {result.operationId && (
                  <p className="font-mono text-xs">
                    Operation: {result.operationId}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </ModalShell>
      )}
    </>
  );
}
