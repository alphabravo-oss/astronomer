import { useState, useMemo } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";
import {
  listQueues,
  listDLQ,
  getDLQOperation,
  retryDLQTask,
  discardDLQTask,
  listTaskOutbox,
  retryTaskOutbox,
  type TaskOutboxStatus,
} from "@/lib/api/admin-operations";
import { useOperationMutation } from "@/lib/hooks/operation-mutation";

export function useOperations() {
  const qc = useQueryClient();
  const queues = useQuery({
    queryKey: queryKeys.adminOperations.queues,
    queryFn: ({ signal }) => listQueues(signal),
    refetchInterval: liveFallback(5_000),
    refetchIntervalInBackground: false,
  });

  // Default to the first queue with non-zero archived count, falling back to
  // the first queue overall so the DLQ panel renders something meaningful on
  // first paint without forcing the operator to click around.
  const queueNames = useMemo(
    () => (queues.data ?? []).map((q) => q.name),
    [queues.data],
  );
  const defaultDLQ = useMemo(() => {
    const withArchived = (queues.data ?? []).find((q) => q.archived > 0);
    return withArchived?.name ?? queueNames[0] ?? "";
  }, [queues.data, queueNames]);
  const [selectedQueue, setSelectedQueue] = useState<string>("");
  const activeQueue = selectedQueue || defaultDLQ;
  const [outboxStatus, setOutboxStatus] = useState<TaskOutboxStatus | "">(
    "dead",
  );

  const dlq = useQuery({
    queryKey: queryKeys.adminOperations.dlq(activeQueue),
    queryFn: ({ signal }) => listDLQ(activeQueue, signal),
    enabled: !!activeQueue,
    refetchInterval: liveFallback(10_000),
  });

  const outbox = useQuery({
    queryKey: queryKeys.adminOperations.outbox(outboxStatus),
    queryFn: ({ signal }) => listTaskOutbox(outboxStatus, signal),
    // `admin_queue.changed` routes only the queue summary + DLQ keys, not the
    // task outbox, so this list polls regardless of stream state.
    refetchInterval: 10_000,
  });

  const retry = useOperationMutation({
    keyPrefix: "dlq-retry",
    submit: ({ queue, id }: { queue: string; id: string }, context) =>
      retryDLQTask(queue, id, context),
    read: getDLQOperation,
    mutation: {
      onSuccess: (_, vars) => {
        toastSuccess(`Retry completed (${vars.id.slice(0, 8)}…)`);
        qc.invalidateQueries({
          queryKey: queryKeys.adminOperations.dlq(vars.queue),
        });
        qc.invalidateQueries({ queryKey: queryKeys.adminOperations.queues });
      },
      onError: (e) => toastApiError("Retry failed", e),
    },
  });
  const discard = useOperationMutation({
    keyPrefix: "dlq-discard",
    submit: ({ queue, id }: { queue: string; id: string }, context) =>
      discardDLQTask(queue, id, context),
    read: getDLQOperation,
    mutation: {
      onSuccess: (_, vars) => {
        toastSuccess(`Discard completed (${vars.id.slice(0, 8)}…)`);
        qc.invalidateQueries({
          queryKey: queryKeys.adminOperations.dlq(vars.queue),
        });
        qc.invalidateQueries({ queryKey: queryKeys.adminOperations.queues });
      },
      onError: (e) => toastApiError("Discard failed", e),
    },
  });
  const retryOutbox = useMutation({
    mutationFn: (id: string) => retryTaskOutbox(id),
    onSuccess: (row) => {
      toastSuccess(`Task outbox row queued (${row.id.slice(0, 8)}…)`);
      qc.invalidateQueries({
        queryKey: queryKeys.adminOperations.outbox(outboxStatus),
      });
    },
    onError: (e) => toastApiError("Outbox retry failed", e),
  });

  return {
    queues,
    dlq,
    outbox,
    retry,
    discard,
    retryOutbox,
    activeQueue,
    setSelectedQueue,
    outboxStatus,
    setOutboxStatus,
  };
}
