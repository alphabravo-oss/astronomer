import { ActionButton } from "@/components/ui/action-button";
import { capitalize, compatibilityLabel } from "./-columns";
import { useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Download,
  History,
  Loader2,
  Send,
  Stethoscope,
  Wrench,
  Unplug,
  X,
} from "lucide-react";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { StatusBadge } from "@/components/ui/status-badge";
import { QueryStates, type QueryState } from "@/components/ui/query-states";
import { cn, formatRelativeTime } from "@/lib/utils";
import type {
  AgentDiagnosticsResponse,
  ClusterAgentItem,
  AgentLifecycleOperation,
  AgentLifecycleOperationsResponse,
  AgentSelfTestResponse,
  AgentUpgradeOperationResponse,
  AgentUpgradePlanResponse,
} from "@/types";

export function AgentDiagnosticsDrawer({
  diagnosticsQuery,
  upgradePlan,
  operationsQuery,
  canManage,
  onClose,
  onPlan,
  onSelfTest,
  onQueue,
  onDownload,
}: {
  diagnosticsQuery: QueryState<AgentDiagnosticsResponse>;
  upgradePlan: AgentUpgradePlanResponse | null;
  operationsQuery: QueryState<AgentLifecycleOperationsResponse>;
  canManage: boolean;
  onClose: () => void;
  onPlan: () => Promise<void>;
  onSelfTest: () => Promise<AgentSelfTestResponse>;
  onQueue: () => Promise<AgentUpgradeOperationResponse>;
  onDownload: () => Promise<void>;
}) {
  const diagnostics = diagnosticsQuery.isError
    ? undefined
    : diagnosticsQuery.data;
  const loading =
    diagnosticsQuery.isLoading || diagnosticsQuery.isError || !diagnostics;
  const [planning, setPlanning] = useState(false);
  const [selfTesting, setSelfTesting] = useState(false);
  const [selfTest, setSelfTest] = useState<AgentSelfTestResponse | null>(null);
  const [queueing, setQueueing] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [queuedOperation, setQueuedOperation] =
    useState<AgentUpgradeOperationResponse | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  return (
    <DrawerShell
      title="Agent diagnostics"
      onClose={onClose}
      subtitle={
        diagnostics?.agent.clusterDisplayName ||
        diagnostics?.agent.clusterName ||
        "Status unavailable"
      }
      actions={
        <>
          <ActionButton
            onClick={async () => {
              setSelfTesting(true);
              setActionError(null);
              try {
                const result = await onSelfTest();
                setSelfTest(result);
              } catch (error) {
                setActionError(
                  error instanceof Error
                    ? error.message
                    : "Failed to run agent self-test",
                );
              } finally {
                setSelfTesting(false);
              }
            }}
            loading={selfTesting}
            icon={<Stethoscope className="h-3.5 w-3.5" />}
            disabled={!canManage || loading}
            title={!canManage ? "Requires cluster_agents:update" : undefined}
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
          >
            Self-test
          </ActionButton>
          <ActionButton
            onClick={async () => {
              setDownloading(true);
              setActionError(null);
              try {
                await onDownload();
              } catch (error) {
                setActionError(
                  error instanceof Error
                    ? error.message
                    : "Failed to download diagnostics bundle",
                );
              } finally {
                setDownloading(false);
              }
            }}
            loading={downloading}
            icon={<Download className="h-3.5 w-3.5" />}
            disabled={loading}
            className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
          >
            Bundle
          </ActionButton>
        </>
      }
    >
      <QueryStates
        query={diagnosticsQuery}
        permission="cluster_agents:read"
        loadingTitle="Loading diagnostics"
        errorTitle="Diagnostics unavailable"
      >
        {(diagnostics) => (
          <div className="space-y-5">
            <div className="grid gap-3 sm:grid-cols-3">
              <MiniStat
                label="Status"
                value={capitalize(diagnostics.agent.agentStatus)}
              />
              <MiniStat
                label="Version"
                value={diagnostics.agent.agentVersion || "-"}
                mono
              />
            </div>

            {selfTest && <SelfTestSection result={selfTest} />}
            {diagnostics.agent.offlineBehavior && (
              <OfflineBehaviorSection
                behavior={diagnostics.agent.offlineBehavior}
              />
            )}

            <section className="rounded-md border border-border bg-card p-4">
              <div className="flex items-center justify-between gap-3">
                <h3 className="text-sm font-medium text-foreground">Upgrade</h3>
                <div className="flex flex-wrap items-center gap-2">
                  {canManage && (
                    <ActionButton
                      onClick={async () => {
                        setPlanning(true);
                        setActionError(null);
                        try {
                          await onPlan();
                        } catch (error) {
                          setActionError(
                            error instanceof Error
                              ? error.message
                              : "Failed to build upgrade plan",
                          );
                        } finally {
                          setPlanning(false);
                        }
                      }}
                      loading={planning}
                      icon={<Wrench className="h-3.5 w-3.5" />}
                      className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
                    >
                      Plan
                    </ActionButton>
                  )}
                  {canManage && (
                    <ActionButton
                      onClick={async () => {
                        setQueueing(true);
                        setActionError(null);
                        try {
                          const result = await onQueue();
                          setQueuedOperation(result);
                        } catch (error) {
                          setActionError(
                            error instanceof Error
                              ? error.message
                              : "Failed to queue upgrade",
                          );
                        } finally {
                          setQueueing(false);
                        }
                      }}
                      loading={queueing}
                      icon={<Send className="h-3.5 w-3.5" />}
                      disabled={!upgradePlan?.ready}
                      className="inline-flex items-center gap-1.5 rounded-md bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
                    >
                      Queue
                    </ActionButton>
                  )}
                </div>
              </div>
              <p className="mt-2 text-sm text-muted-foreground">
                {diagnostics.upgradeRecommendation.message}
              </p>
              {upgradePlan && <UpgradePlan plan={upgradePlan} />}
              {queuedOperation && (
                <p className="mt-3 rounded-md border border-status-success/20 bg-status-success/10 px-3 py-2 text-xs text-status-success">
                  Queued operation {queuedOperation.operation.id.slice(0, 8)}
                </p>
              )}
              {actionError && (
                <p className="mt-3 rounded-md border border-status-error/20 bg-status-error/10 px-3 py-2 text-xs text-status-error">
                  {actionError}
                </p>
              )}
            </section>

            <ListSection
              title="Recommendations"
              items={diagnostics.recommendations}
              empty="No recommendations."
            />
            <ListSection
              title="Redactions"
              items={diagnostics.redactions}
              empty="No redaction notes."
            />
            <QueryStates
              query={operationsQuery}
              permission="cluster_agents:read"
              loadingTitle="Loading operations"
              errorTitle="Operation history unavailable"
            >
              {(page) => (
                <AgentOperationsSection
                  operations={page.data}
                  loading={false}
                />
              )}
            </QueryStates>

            <AgentConnectionHistory diagnostics={diagnostics} />
          </div>
        )}
      </QueryStates>
    </DrawerShell>
  );
}

function OfflineBehaviorSection({
  behavior,
}: {
  behavior: NonNullable<ClusterAgentItem["offlineBehavior"]>;
}) {
  return (
    <section className="rounded-md border border-status-warning/30 bg-status-warning/10 p-4">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">
          Offline behavior
        </h3>
        <Unplug className="h-4 w-4 text-status-warning" />
      </div>
      <p className="mt-2 text-sm text-muted-foreground">{behavior.message}</p>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <MiniStat
          label="Last known"
          value={
            behavior.lastKnownAt
              ? formatRelativeTime(behavior.lastKnownAt)
              : "Never observed"
          }
        />
        <MiniStat
          label="State"
          value={behavior.stale ? "Stale offline" : "Recently offline"}
        />
      </div>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <InlineOperationList
          title="Queueable"
          items={behavior.permittedQueuedOperations.map(formatOperationLabel)}
          empty="No queued operations are safe while offline."
        />
        <InlineOperationList
          title="Blocked"
          items={behavior.blockedOperations.map(formatOperationLabel)}
          empty="No blocked operations."
        />
      </div>
    </section>
  );
}

function SelfTestSection({ result }: { result: AgentSelfTestResponse }) {
  return (
    <section className="rounded-md border border-border bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">Self-test</h3>
        <span
          className={cn(
            "inline-flex items-center rounded-sm border px-1.5 py-0.5 text-xs font-medium",
            selfTestTone(result.status),
          )}
        >
          {compatibilityLabel(result.status)}
        </span>
      </div>
      <div className="mt-3 space-y-2">
        {result.checks.map((check) => (
          <div
            key={check.name}
            className="rounded-md bg-muted/40 px-3 py-2 text-xs"
          >
            <div className="flex items-center gap-2">
              {check.status === "passed" ? (
                <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-status-success" />
              ) : check.status === "failed" ? (
                <X className="h-3.5 w-3.5 shrink-0 text-status-error" />
              ) : (
                <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-warning" />
              )}
              <span className="font-medium text-foreground">
                {check.name.replaceAll("_", " ")}
              </span>
              <span
                className={cn(
                  "ml-auto font-medium",
                  selfTestTextTone(check.status),
                )}
              >
                {compatibilityLabel(check.status)}
              </span>
            </div>
            <p className="mt-1 text-muted-foreground">{check.message}</p>
          </div>
        ))}
      </div>
      {result.recommendations?.length ? (
        <ul className="mt-3 space-y-1 text-xs text-muted-foreground">
          {result.recommendations.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function selfTestTone(value: string): string {
  if (value === "passed")
    return "border-status-success/30 bg-status-success/10 text-status-success";
  if (value === "failed")
    return "border-status-error/30 bg-status-error/10 text-status-error";
  return "border-status-warning/30 bg-status-warning/10 text-status-warning";
}

function selfTestTextTone(value: string): string {
  if (value === "passed") return "text-status-success";
  if (value === "failed") return "text-status-error";
  return "text-status-warning";
}

function AgentOperationsSection({
  operations,
  loading,
}: {
  operations: AgentLifecycleOperation[];
  loading: boolean;
}) {
  return (
    <section className="rounded-md border border-border bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">
          Lifecycle operations
        </h3>
        <History className="h-4 w-4 text-muted-foreground" />
      </div>
      {loading ? (
        <div className="mt-3 flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading
        </div>
      ) : operations.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">
          No lifecycle operations.
        </p>
      ) : (
        <div className="mt-3 space-y-2">
          {operations.map((operation) => (
            <div
              key={operation.id}
              className="rounded-md bg-muted/40 px-3 py-2 text-xs"
            >
              <div className="flex items-center justify-between gap-3">
                <span className="font-mono text-foreground">
                  {operation.targetVersion}
                </span>
                <StatusBadge
                  status={operation.status}
                  label={capitalize(operation.status)}
                />
              </div>
              <p className="mt-1 truncate font-mono text-muted-foreground">
                {operation.targetImage}
              </p>
              <p className="mt-1 text-muted-foreground">
                {operation.operationType.replace("_", " ")} ·{" "}
                {formatRelativeTime(operation.createdAt)}
              </p>
              {operation.lastError && (
                <p className="mt-1 text-status-error">{operation.lastError}</p>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function MiniStat({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="rounded-md border border-border bg-card px-3 py-2">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p
        className={cn(
          "mt-1 truncate text-sm font-medium text-foreground",
          mono && "font-mono text-xs",
        )}
      >
        {value}
      </p>
    </div>
  );
}

function ListSection({
  title,
  items,
  empty,
}: {
  title: string;
  items: string[];
  empty: string;
}) {
  return (
    <section className="rounded-md border border-border bg-card p-4">
      <h3 className="text-sm font-medium text-foreground">{title}</h3>
      {items.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <ul className="mt-3 space-y-2">
          {items.map((item) => (
            <li
              key={item}
              className="rounded-md bg-muted/40 px-3 py-2 text-sm text-muted-foreground"
            >
              {item}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function InlineOperationList({
  title,
  items,
  empty,
}: {
  title: string;
  items: string[];
  empty: string;
}) {
  return (
    <div>
      <h4 className="text-xs font-medium uppercase text-muted-foreground">
        {title}
      </h4>
      {items.length === 0 ? (
        <p className="mt-2 text-xs text-muted-foreground">{empty}</p>
      ) : (
        <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
          {items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function formatOperationLabel(value: string): string {
  return value.replaceAll("_", " ");
}

function UpgradePlan({ plan }: { plan: AgentUpgradePlanResponse }) {
  return (
    <div className="mt-4 rounded-md border border-border bg-background p-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-medium text-muted-foreground">
          Target
        </span>
        <span className="font-mono text-xs text-foreground">
          {plan.targetImage}
        </span>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-3">
        <MiniStat label="Batch" value={`${plan.batchSize || 1}`} />
        <MiniStat
          label="Max unavailable"
          value={`${plan.maxUnavailable || 1}`}
        />
        <MiniStat
          label="Canaries"
          value={`${plan.canaryClusterIds?.length ?? 0}`}
        />
      </div>
      {plan.rollbackImage && (
        <div className="mt-3 flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2">
          <span className="text-xs font-medium text-muted-foreground">
            Rollback
          </span>
          <span className="truncate font-mono text-xs text-foreground">
            {plan.rollbackImage}
          </span>
        </div>
      )}
      {!plan.ready && plan.blockers?.length ? (
        <div className="mt-3 rounded-md border border-status-warning/20 bg-status-warning/10 px-3 py-2">
          <p className="text-xs font-medium text-status-warning">Blocked</p>
          <ul className="mt-1 space-y-1 text-xs text-status-warning">
            {plan.blockers.map((blocker) => (
              <li key={blocker}>{blocker}</li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="mt-3 text-xs text-status-success">
          Ready for manifest rollout
        </p>
      )}
      <div className="mt-3 grid gap-3 md:grid-cols-2 xl:grid-cols-5">
        <PlanList title="Preflight" items={plan.preflightChecks} />
        <PlanList title="Steps" items={plan.steps} />
        <PlanList title="Post-checks" items={plan.postUpgradeHealthChecks} />
        <PlanList title="Validate" items={plan.validation} />
        <PlanList title="Rollback" items={plan.rollback} />
      </div>
    </div>
  );
}

function PlanList({ title, items }: { title: string; items: string[] }) {
  return (
    <div>
      <p className="text-xs font-medium text-foreground">{title}</p>
      <ol className="mt-1 space-y-1 text-xs text-muted-foreground">
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ol>
    </div>
  );
}

function AgentConnectionHistory({
  diagnostics,
}: {
  diagnostics: AgentDiagnosticsResponse;
}) {
  return (
    <>
      <section className="rounded-md border border-border bg-card p-4">
        <h3 className="text-sm font-medium text-foreground">
          Recent connections
        </h3>
        <div className="mt-3 space-y-2">
          {diagnostics.recentConnections.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No connection history.
            </p>
          ) : (
            diagnostics.recentConnections.map((conn) => (
              <div
                key={conn.id}
                className="rounded-md bg-muted/40 px-3 py-2 text-xs"
              >
                <div className="flex items-center justify-between gap-3">
                  <span className="font-mono text-foreground">
                    {conn.agentId || "-"}
                  </span>
                  <StatusBadge
                    status={
                      conn.status === "connected" ? "connected" : "disconnected"
                    }
                    label={conn.status}
                  />
                </div>
                <p className="mt-1 text-muted-foreground">
                  connected {formatRelativeTime(conn.connectedAt)}
                  {conn.lastPing
                    ? `, ping ${formatRelativeTime(conn.lastPing)}`
                    : ""}
                </p>
              </div>
            ))
          )}
        </div>
      </section>

      <section className="rounded-md border border-border bg-card p-4">
        <h3 className="text-sm font-medium text-foreground">Conditions</h3>
        <div className="mt-3 space-y-2">
          {diagnostics.conditions.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No cluster conditions recorded.
            </p>
          ) : (
            diagnostics.conditions.map((condition) => (
              <div
                key={`${condition.type}-${condition.lastTransitionTime}`}
                className="rounded-md bg-muted/40 px-3 py-2 text-xs"
              >
                <div className="flex items-center justify-between gap-3">
                  <span className="font-medium text-foreground">
                    {condition.type}
                  </span>
                  <StatusBadge
                    status={condition.status === "True" ? "active" : "warning"}
                    label={condition.status}
                  />
                </div>
                {condition.message && (
                  <p className="mt-1 text-muted-foreground">
                    {condition.message}
                  </p>
                )}
              </div>
            ))
          )}
        </div>
      </section>
    </>
  );
}
