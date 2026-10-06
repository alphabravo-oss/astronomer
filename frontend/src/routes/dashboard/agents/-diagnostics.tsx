import { ActionButton } from "@/components/ui/action-button";
import { capitalize } from "./-columns";
import {
  AgentConnectionHistory,
  AgentOperationsSection,
  ListSection,
  MiniStat,
  OfflineBehaviorSection,
  SelfTestSection,
  UpgradePlan,
} from "./-diagnostics-sections";
import { useState } from "react";
import { Download, Send, Stethoscope, Wrench } from "lucide-react";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { QueryStates, type QueryState } from "@/components/ui/query-states";
import type {
  AgentDiagnosticsResponse,
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
            tooltip={!canManage ? "Requires cluster_agents:update" : undefined}
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
