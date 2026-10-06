import { createFileRoute } from "@tanstack/react-router";
/**
 * Operations admin tab (T28b) — surface the asynq queue state + DLQ so on-call
 * can answer "why isn't anything reconciling?" from the UI instead of curl /
 * shelling into a worker pod.
 *
 * Two panels:
 *   1. Queues — depth per queue, refreshed every 5s.
 *   2. Dead-letter — failed tasks per queue with Retry / Discard actions.
 *
 * The retry / discard buttons hit POST /admin/queues/{q}/dlq/{id}/retry/ and
 * DELETE /admin/queues/{q}/dlq/{id}/. Both audited server-side.
 */

import { ResourceMasthead, PageShell } from "@/components/ui/page";
import { RefreshCw, Database } from "lucide-react";
import { Select } from "@/components/ui/select";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import type { TaskOutboxStatus } from "@/lib/api/admin-operations";
import { QueryStates } from "@/components/ui/query-states";
import { BareButton } from "@/components/form/bare-button";
import { DLQTable, QueueTable, TaskOutboxTable } from "./-tables";
import { useOperations } from "./-use-operations";

function OperationsBody() {
  const {
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
  } = useOperations();

  return (
    <PageShell>
      <p className="sr-only" role="status" aria-live="polite">
        {retry.isPending
          ? `DLQ retry ${retry.operationState.phase}`
          : discard.isPending
            ? `DLQ discard ${discard.operationState.phase}`
            : ""}
      </p>
      <ResourceMasthead
        backTo="/dashboard/settings"
        backLabel="Back to Settings"
        title="Operations"
        description="Live view of the asynq worker queues + DLQ. Audited; superuser-only."
      />

      {queues.isError && (
        <QueryStates query={queues} permission="admin_operations:read">
          {() => null}
        </QueryStates>
      )}
      {dlq.isError && (
        <QueryStates query={dlq} permission="admin_operations:read">
          {() => null}
        </QueryStates>
      )}
      {outbox.isError && (
        <QueryStates query={outbox} permission="admin_operations:read">
          {() => null}
        </QueryStates>
      )}

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-foreground">Queues</h2>
          <BareButton
            onClick={() => queues.refetch()}
            className="inline-flex items-center gap-1.5 h-7 px-2 rounded-sm text-xs border border-border hover:bg-accent"
            tooltip="Refresh now"
          >
            <RefreshCw
              className={`h-3 w-3 ${queues.isFetching ? "animate-spin" : ""}`}
            />{" "}
            Refresh
          </BareButton>
        </div>
        <QueueTable
          loading={queues.isLoading}
          rows={queues.data ?? []}
          activeQueue={activeQueue}
          onSelect={setSelectedQueue}
        />
      </section>

      <section className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-foreground">
            Dead-letter
            {activeQueue && (
              <span className="ml-2 text-xs text-muted-foreground font-mono">
                — {activeQueue}
              </span>
            )}
          </h2>
          <BareButton
            onClick={() => dlq.refetch()}
            disabled={!activeQueue}
            className="inline-flex items-center gap-1.5 h-7 px-2 rounded-sm text-xs border border-border hover:bg-accent disabled:opacity-50"
            tooltip="Refresh DLQ"
          >
            <RefreshCw
              className={`h-3 w-3 ${dlq.isFetching ? "animate-spin" : ""}`}
            />{" "}
            Refresh
          </BareButton>
        </div>
        <DLQTable
          loading={dlq.isLoading && !!activeQueue}
          queue={activeQueue}
          rows={dlq.data?.dlq ?? []}
          onRetry={(id) => retry.mutate({ queue: activeQueue, id })}
          onDiscard={(id) => discard.mutate({ queue: activeQueue, id })}
          pendingRetry={retry.isPending}
          pendingDiscard={discard.isPending}
        />
      </section>

      <section className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="text-sm font-medium text-foreground inline-flex items-center gap-2">
              <Database className="h-4 w-4" />
              Task outbox
            </h2>
            <p className="text-xs text-muted-foreground mt-1">
              Durable DB task intents waiting for Redis delivery or operator
              retry.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Select
              value={outboxStatus}
              onChange={(e) =>
                setOutboxStatus(e.target.value as TaskOutboxStatus | "")
              }
              className="h-8 rounded-sm border border-border bg-background px-2 text-xs"
              aria-label="Filter task outbox rows"
            >
              <option value="dead">Dead</option>
              <option value="failed">Failed</option>
              <option value="pending">Pending</option>
              <option value="delivering">Delivering</option>
              <option value="delivered">Delivered</option>
              <option value="">All</option>
            </Select>
            <BareButton
              onClick={() => outbox.refetch()}
              className="inline-flex items-center gap-1.5 h-7 px-2 rounded-sm text-xs border border-border hover:bg-accent"
              tooltip="Refresh task outbox"
            >
              <RefreshCw
                className={`h-3 w-3 ${outbox.isFetching ? "animate-spin" : ""}`}
              />{" "}
              Refresh
            </BareButton>
          </div>
        </div>
        <TaskOutboxTable
          loading={outbox.isLoading}
          rows={outbox.data?.data ?? []}
          status={outboxStatus}
          onRetry={(id) => retryOutbox.mutate(id)}
          pendingRetry={retryOutbox.isPending}
        />
      </section>
    </PageShell>
  );
}

function OperationsPage() {
  return (
    <SettingsAuthGate>
      <div className="p-6">
        <OperationsBody />
      </div>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/operations/")({
  component: OperationsPage,
});
