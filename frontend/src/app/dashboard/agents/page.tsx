'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Activity, AlertTriangle, CheckCircle2, Download, History, Loader2, Send, Server, Shield, Stethoscope, Unplug, Wrench, X } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { DrawerShell } from '@/components/ui/drawer-shell';
import { StatusBadge } from '@/components/ui/status-badge';
import { createAgentUpgradeOperation, createAgentUpgradePlan, downloadAgentDiagnosticsBundle, getAgentDiagnostics, getAgentFleet, getAgentOperations, runAgentSelfTest } from '@/lib/api';
import { queryKeys } from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live-events';
import { cn, formatRelativeTime } from '@/lib/utils';
import type { AgentDiagnosticsResponse, AgentFleetItem, AgentLifecycleOperation, AgentSelfTestResponse, AgentUpgradeOperationResponse, AgentUpgradePlanResponse } from '@/types';

export default function AgentFleetPage() {
  const [selectedClusterId, setSelectedClusterId] = useState<string | null>(null);
  const [upgradePlan, setUpgradePlan] = useState<AgentUpgradePlanResponse | null>(null);
  const { data, isLoading } = useQuery({
    queryKey: queryKeys.agents.fleet,
    queryFn: () => getAgentFleet({ limit: 250 }),
    refetchInterval: 30000,
  });

  useLiveQueryInvalidation(
    ['cluster.connected', 'cluster.disconnected', 'cluster.heartbeat', 'agent.reconnecting', 'agent.failed'],
    [queryKeys.agents.fleet],
  );

  const items = data?.items ?? [];
  const summary = data?.summary;
  const versionEntries = Object.entries(summary?.versions ?? {}).sort((a, b) => b[1] - a[1]);
  const profileEntries = Object.entries(summary?.profiles ?? {}).sort((a, b) => b[1] - a[1]);
  const compatibilityEntries = Object.entries(summary?.compatibility ?? {}).sort((a, b) => b[1] - a[1]);
  const diagnostics = useQuery({
    queryKey: queryKeys.agents.diagnostics(selectedClusterId),
    queryFn: () => getAgentDiagnostics(selectedClusterId!),
    enabled: !!selectedClusterId,
  });
  const operations = useQuery({
    queryKey: queryKeys.agents.operations(selectedClusterId),
    queryFn: () => getAgentOperations(selectedClusterId!, { limit: 10 }),
    enabled: !!selectedClusterId,
    refetchInterval: selectedClusterId ? 15000 : false,
  });

  const columns: Column<AgentFleetItem>[] = [
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.clusterDisplayName || row.clusterName}</p>
          <p className="text-xs text-muted-foreground font-mono">{row.clusterId}</p>
        </div>
      ),
      sortAccessor: (row) => row.clusterDisplayName || row.clusterName,
    },
    {
      key: 'agentStatus',
      header: 'Agent',
      accessor: (row) => (
        <div className="space-y-1">
          <StatusBadge status={row.agentStatus} label={capitalize(row.agentStatus)} />
          {row.degradedReasons?.length ? (
            <p className="text-xs text-status-warning">{row.degradedReasons[0]}</p>
          ) : null}
        </div>
      ),
      sortAccessor: (row) => row.agentStatus,
    },
    {
      key: 'version',
      header: 'Version',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.agentVersion || '-'}</span>
      ),
      sortAccessor: (row) => row.agentVersion || '',
    },
    {
      key: 'compatibility',
      header: 'Compatibility',
      accessor: (row) => (
        <span
          title={row.compatibilityMessage}
          className={cn(
            'inline-flex items-center rounded border px-1.5 py-0.5 text-xs font-medium',
            compatibilityTone(row.compatibilityStatus),
          )}
        >
          {compatibilityLabel(row.compatibilityStatus)}
        </span>
      ),
      sortAccessor: (row) => row.compatibilityStatus,
    },
    {
      key: 'profile',
      header: 'Profile',
      accessor: (row) => (
        <span
          className={cn(
            'inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium',
            row.privilegeProfile === 'admin'
              ? 'bg-status-warning/10 text-status-warning'
              : 'bg-muted text-muted-foreground',
          )}
        >
          <Shield className="h-3 w-3" />
          {row.privilegeProfile}
        </span>
      ),
      sortAccessor: (row) => row.privilegeProfile,
    },
    {
      key: 'capabilities',
      header: 'Capabilities',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {Object.entries(row.capabilities)
            .filter(([, enabled]) => enabled)
            .slice(0, 4)
            .map(([name]) => (
              <span key={name} className="rounded bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground">
                {name.replace('_', ' ')}
              </span>
            ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'kubernetes',
      header: 'Kubernetes',
      accessor: (row) => (
        <div className="text-xs text-muted-foreground">
          <p className="font-mono">{row.kubernetesVersion || '-'}</p>
          <p>{row.nodeCount} nodes</p>
        </div>
      ),
      sortAccessor: (row) => row.kubernetesVersion || '',
    },
    {
      key: 'lastHeartbeat',
      header: 'Last Heartbeat',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : '-'}
        </span>
      ),
      sortAccessor: (row) => row.lastHeartbeat || '',
    },
    {
      key: 'session',
      header: 'Session',
      accessor: (row) => (
        <div className="text-xs text-muted-foreground">
          <p className="font-mono">{row.agentId || '-'}</p>
          {row.podName ? <p>{row.podName}</p> : null}
        </div>
      ),
      sortAccessor: (row) => row.agentId || '',
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <button
          onClick={(event) => {
            event.stopPropagation();
            setSelectedClusterId(row.clusterId);
            setUpgradePlan(null);
          }}
          className="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <Stethoscope className="h-3.5 w-3.5" />
          Diagnostics
        </button>
      ),
      sortable: false,
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Agent Fleet</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Connected, degraded, and disconnected Astronomer agents across adopted clusters.
          </p>
        </div>
        <div className="text-xs text-muted-foreground">
          Server {summary?.serverVersion || '-'} · Supported agent {summary?.minimumSupportedAgentVersion || '-'} · Compatible {summary?.minimumCompatibleAgentVersion || '-'} · Generated {summary?.generatedAt ? formatRelativeTime(summary.generatedAt) : '-'}
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <SummaryTile icon={Server} label="Clusters" value={summary?.totalClusters ?? 0} />
        <SummaryTile icon={CheckCircle2} label="Connected" value={summary?.connected ?? 0} tone="success" />
        <SummaryTile icon={AlertTriangle} label="Degraded" value={summary?.degraded ?? 0} tone="warning" />
        <SummaryTile icon={Unplug} label="Disconnected" value={summary?.disconnected ?? 0} tone="neutral" />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <DistributionPanel title="Versions" entries={versionEntries} empty="No agent versions reported" />
        <DistributionPanel title="Privilege Profiles" entries={profileEntries} empty="No privilege profiles reported" />
        <DistributionPanel title="Compatibility" entries={compatibilityEntries} empty="No compatibility data" />
      </div>

      <DataTable
        data={items}
        columns={columns}
        keyExtractor={(row) => row.clusterId}
        loading={isLoading}
        searchPlaceholder="Search agents..."
        emptyMessage="No adopted-cluster agents found."
        pageSize={25}
      />
      {selectedClusterId && (
        <AgentDiagnosticsDrawer
          diagnostics={diagnostics.data}
          loading={diagnostics.isLoading}
          upgradePlan={upgradePlan}
          operations={operations.data?.items ?? []}
          operationsLoading={operations.isLoading}
          onClose={() => {
            setSelectedClusterId(null);
            setUpgradePlan(null);
          }}
          onPlan={async () => {
            const plan = await createAgentUpgradePlan(selectedClusterId);
            setUpgradePlan(plan);
          }}
          onSelfTest={() => runAgentSelfTest(selectedClusterId)}
          onQueue={async () => {
            const result = await createAgentUpgradeOperation(selectedClusterId);
            setUpgradePlan(result.plan);
            await operations.refetch();
            return result;
          }}
          onDownload={async () => {
            const blob = await downloadAgentDiagnosticsBundle(selectedClusterId);
            const url = URL.createObjectURL(blob);
            const link = document.createElement('a');
            link.href = url;
            link.download = `astronomer-agent-diagnostics-${selectedClusterId}.json`;
            document.body.appendChild(link);
            link.click();
            link.remove();
            URL.revokeObjectURL(url);
          }}
        />
      )}
    </div>
  );
}

function SummaryTile({
  icon: Icon,
  label,
  value,
  tone = 'default',
}: {
  icon: typeof Activity;
  label: string;
  value: number;
  tone?: 'default' | 'success' | 'warning' | 'neutral';
}) {
  return (
    <div className="rounded-md border border-border bg-card px-4 py-3">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">{label}</p>
        <Icon
          className={cn(
            'h-4 w-4',
            tone === 'success' && 'text-status-success',
            tone === 'warning' && 'text-status-warning',
            tone === 'neutral' && 'text-status-neutral',
            tone === 'default' && 'text-muted-foreground',
          )}
        />
      </div>
      <p className="mt-2 text-2xl font-semibold tabular-nums text-foreground">{value}</p>
    </div>
  );
}

function DistributionPanel({
  title,
  entries,
  empty,
}: {
  title: string;
  entries: Array<[string, number]>;
  empty: string;
}) {
  return (
    <div className="rounded-md border border-border bg-card p-4">
      <h2 className="text-sm font-medium text-foreground">{title}</h2>
      {entries.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <div className="mt-3 space-y-2">
          {entries.map(([name, count]) => (
            <div key={name} className="flex items-center justify-between gap-3 text-sm">
              <span className="truncate font-mono text-xs text-muted-foreground">{name}</span>
              <span className="tabular-nums text-foreground">{count}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function compatibilityLabel(value: string): string {
  if (value === 'supported') return 'Supported';
  if (value === 'deprecated') return 'Deprecated';
  if (value === 'blocked') return 'Blocked';
  if (value === 'unknown') return 'Unknown';
  return capitalize(value || 'unknown');
}

function compatibilityTone(value: string): string {
  if (value === 'supported') return 'border-status-success/30 bg-status-success/10 text-status-success';
  if (value === 'blocked') return 'border-status-danger/30 bg-status-danger/10 text-status-danger';
  if (value === 'deprecated') return 'border-status-warning/30 bg-status-warning/10 text-status-warning';
  return 'border-border bg-muted/30 text-muted-foreground';
}

function AgentDiagnosticsDrawer({
  diagnostics,
  loading,
  upgradePlan,
  operations,
  operationsLoading,
  onClose,
  onPlan,
  onSelfTest,
  onQueue,
  onDownload,
}: {
  diagnostics?: AgentDiagnosticsResponse;
  loading: boolean;
  upgradePlan: AgentUpgradePlanResponse | null;
  operations: AgentLifecycleOperation[];
  operationsLoading: boolean;
  onClose: () => void;
  onPlan: () => Promise<void>;
  onSelfTest: () => Promise<AgentSelfTestResponse>;
  onQueue: () => Promise<AgentUpgradeOperationResponse>;
  onDownload: () => Promise<void>;
}) {
  const [planning, setPlanning] = useState(false);
  const [selfTesting, setSelfTesting] = useState(false);
  const [selfTest, setSelfTest] = useState<AgentSelfTestResponse | null>(null);
  const [queueing, setQueueing] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [queuedOperation, setQueuedOperation] = useState<AgentUpgradeOperationResponse | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  return (
    <DrawerShell
      title="Agent diagnostics"
      onClose={onClose}
      subtitle={diagnostics?.agent.clusterDisplayName || diagnostics?.agent.clusterName || 'Loading'}
      actions={(
        <>
            <button
              onClick={async () => {
                setSelfTesting(true);
                setActionError(null);
                try {
                  const result = await onSelfTest();
                  setSelfTest(result);
                } catch (error) {
                  setActionError(error instanceof Error ? error.message : 'Failed to run agent self-test');
                } finally {
                  setSelfTesting(false);
                }
              }}
              disabled={loading || selfTesting}
              className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
            >
              {selfTesting ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Stethoscope className="h-3.5 w-3.5" />}
              Self-test
            </button>
            <button
              onClick={async () => {
                setDownloading(true);
                setActionError(null);
                try {
                  await onDownload();
                } catch (error) {
                  setActionError(error instanceof Error ? error.message : 'Failed to download diagnostics bundle');
                } finally {
                  setDownloading(false);
                }
              }}
              disabled={loading || downloading}
              className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
            >
              {downloading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}
              Bundle
            </button>
        </>
      )}
    >
          {loading || !diagnostics ? (
            <div className="flex h-48 items-center justify-center text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
            </div>
          ) : (
            <div className="space-y-5">
              <div className="grid gap-3 sm:grid-cols-3">
                <MiniStat label="Status" value={capitalize(diagnostics.agent.agentStatus)} />
                <MiniStat label="Version" value={diagnostics.agent.agentVersion || '-'} mono />
                <MiniStat label="Profile" value={diagnostics.agent.privilegeProfile} />
              </div>

              {selfTest && <SelfTestSection result={selfTest} />}
              {diagnostics.agent.offlineBehavior && <OfflineBehaviorSection behavior={diagnostics.agent.offlineBehavior} />}

              <section className="rounded-md border border-border bg-card p-4">
                <div className="flex items-center justify-between gap-3">
                  <h3 className="text-sm font-medium text-foreground">Upgrade</h3>
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      onClick={async () => {
                        setPlanning(true);
                        setActionError(null);
                        try {
                          await onPlan();
                        } catch (error) {
                          setActionError(error instanceof Error ? error.message : 'Failed to build upgrade plan');
                        } finally {
                          setPlanning(false);
                        }
                      }}
                      disabled={planning}
                      className="inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
                    >
                      {planning ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Wrench className="h-3.5 w-3.5" />}
                      Plan
                    </button>
                    <button
                      onClick={async () => {
                        setQueueing(true);
                        setActionError(null);
                        try {
                          const result = await onQueue();
                          setQueuedOperation(result);
                        } catch (error) {
                          setActionError(error instanceof Error ? error.message : 'Failed to queue upgrade');
                        } finally {
                          setQueueing(false);
                        }
                      }}
                      disabled={!upgradePlan?.ready || queueing}
                      className="inline-flex items-center gap-1.5 rounded-md bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
                    >
                      {queueing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Send className="h-3.5 w-3.5" />}
                      Queue
                    </button>
                  </div>
                </div>
                <p className="mt-2 text-sm text-muted-foreground">{diagnostics.upgradeRecommendation.message}</p>
                {upgradePlan && <UpgradePlan plan={upgradePlan} />}
                {queuedOperation && (
                  <p className="mt-3 rounded-md border border-status-success/20 bg-status-success/10 px-3 py-2 text-xs text-status-success">
                    Queued operation {queuedOperation.operation.id.slice(0, 8)}
                  </p>
                )}
                {actionError && (
                  <p className="mt-3 rounded-md border border-status-danger/20 bg-status-danger/10 px-3 py-2 text-xs text-status-danger">
                    {actionError}
                  </p>
                )}
              </section>

              <ListSection title="Recommendations" items={diagnostics.recommendations} empty="No recommendations." />
              <ListSection title="Redactions" items={diagnostics.redactions} empty="No redaction notes." />
              <AgentOperationsSection operations={operations} loading={operationsLoading} />

              <section className="rounded-md border border-border bg-card p-4">
                <h3 className="text-sm font-medium text-foreground">Recent connections</h3>
                <div className="mt-3 space-y-2">
                  {diagnostics.recentConnections.length === 0 ? (
                    <p className="text-sm text-muted-foreground">No connection history.</p>
                  ) : diagnostics.recentConnections.map((conn) => (
                    <div key={conn.id} className="rounded-md bg-muted/40 px-3 py-2 text-xs">
                      <div className="flex items-center justify-between gap-3">
                        <span className="font-mono text-foreground">{conn.agentId || '-'}</span>
                        <StatusBadge status={conn.status === 'connected' ? 'connected' : 'disconnected'} label={conn.status} />
                      </div>
                      <p className="mt-1 text-muted-foreground">
                        connected {formatRelativeTime(conn.connectedAt)}
                        {conn.lastPing ? `, ping ${formatRelativeTime(conn.lastPing)}` : ''}
                      </p>
                    </div>
                  ))}
                </div>
              </section>

              <section className="rounded-md border border-border bg-card p-4">
                <h3 className="text-sm font-medium text-foreground">Conditions</h3>
                <div className="mt-3 space-y-2">
                  {diagnostics.conditions.length === 0 ? (
                    <p className="text-sm text-muted-foreground">No cluster conditions recorded.</p>
                  ) : diagnostics.conditions.map((condition) => (
                    <div key={`${condition.type}-${condition.lastTransitionTime}`} className="rounded-md bg-muted/40 px-3 py-2 text-xs">
                      <div className="flex items-center justify-between gap-3">
                        <span className="font-medium text-foreground">{condition.type}</span>
                        <StatusBadge status={condition.status === 'True' ? 'active' : 'warning'} label={condition.status} />
                      </div>
                      {condition.message && <p className="mt-1 text-muted-foreground">{condition.message}</p>}
                    </div>
                  ))}
                </div>
              </section>
            </div>
          )}
    </DrawerShell>
  );
}

function OfflineBehaviorSection({ behavior }: { behavior: NonNullable<AgentFleetItem['offlineBehavior']> }) {
  return (
    <section className="rounded-md border border-status-warning/30 bg-status-warning/10 p-4">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">Offline behavior</h3>
        <Unplug className="h-4 w-4 text-status-warning" />
      </div>
      <p className="mt-2 text-sm text-muted-foreground">{behavior.message}</p>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <MiniStat label="Last known" value={behavior.lastKnownAt ? formatRelativeTime(behavior.lastKnownAt) : 'Never observed'} />
        <MiniStat label="State" value={behavior.stale ? 'Stale offline' : 'Recently offline'} />
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
            'inline-flex items-center rounded border px-1.5 py-0.5 text-xs font-medium',
            selfTestTone(result.status),
          )}
        >
          {compatibilityLabel(result.status)}
        </span>
      </div>
      <div className="mt-3 space-y-2">
        {result.checks.map((check) => (
          <div key={check.name} className="rounded-md bg-muted/40 px-3 py-2 text-xs">
            <div className="flex items-center gap-2">
              {check.status === 'passed' ? (
                <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-status-success" />
              ) : check.status === 'failed' ? (
                <X className="h-3.5 w-3.5 shrink-0 text-status-danger" />
              ) : (
                <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-status-warning" />
              )}
              <span className="font-medium text-foreground">{check.name.replaceAll('_', ' ')}</span>
              <span className={cn('ml-auto font-medium', selfTestTextTone(check.status))}>
                {compatibilityLabel(check.status)}
              </span>
            </div>
            <p className="mt-1 text-muted-foreground">{check.message}</p>
          </div>
        ))}
      </div>
      {result.recommendations?.length ? (
        <ul className="mt-3 space-y-1 text-xs text-muted-foreground">
          {result.recommendations.map((item) => <li key={item}>{item}</li>)}
        </ul>
      ) : null}
    </section>
  );
}

function selfTestTone(value: string): string {
  if (value === 'passed') return 'border-status-success/30 bg-status-success/10 text-status-success';
  if (value === 'failed') return 'border-status-danger/30 bg-status-danger/10 text-status-danger';
  return 'border-status-warning/30 bg-status-warning/10 text-status-warning';
}

function selfTestTextTone(value: string): string {
  if (value === 'passed') return 'text-status-success';
  if (value === 'failed') return 'text-status-danger';
  return 'text-status-warning';
}

function AgentOperationsSection({ operations, loading }: { operations: AgentLifecycleOperation[]; loading: boolean }) {
  return (
    <section className="rounded-md border border-border bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-medium text-foreground">Lifecycle operations</h3>
        <History className="h-4 w-4 text-muted-foreground" />
      </div>
      {loading ? (
        <div className="mt-3 flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading
        </div>
      ) : operations.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">No lifecycle operations.</p>
      ) : (
        <div className="mt-3 space-y-2">
          {operations.map((operation) => (
            <div key={operation.id} className="rounded-md bg-muted/40 px-3 py-2 text-xs">
              <div className="flex items-center justify-between gap-3">
                <span className="font-mono text-foreground">{operation.targetVersion}</span>
                <StatusBadge status={operation.status} label={capitalize(operation.status)} />
              </div>
              <p className="mt-1 truncate font-mono text-muted-foreground">{operation.targetImage}</p>
              <p className="mt-1 text-muted-foreground">
                {operation.operationType.replace('_', ' ')} · {formatRelativeTime(operation.createdAt)}
              </p>
              {operation.lastError && <p className="mt-1 text-status-danger">{operation.lastError}</p>}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function MiniStat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-md border border-border bg-card px-3 py-2">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className={cn('mt-1 truncate text-sm font-medium text-foreground', mono && 'font-mono text-xs')}>{value}</p>
    </div>
  );
}

function ListSection({ title, items, empty }: { title: string; items: string[]; empty: string }) {
  return (
    <section className="rounded-md border border-border bg-card p-4">
      <h3 className="text-sm font-medium text-foreground">{title}</h3>
      {items.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <ul className="mt-3 space-y-2">
          {items.map((item) => (
            <li key={item} className="rounded-md bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
              {item}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function InlineOperationList({ title, items, empty }: { title: string; items: string[]; empty: string }) {
  return (
    <div>
      <h4 className="text-xs font-medium uppercase text-muted-foreground">{title}</h4>
      {items.length === 0 ? (
        <p className="mt-2 text-xs text-muted-foreground">{empty}</p>
      ) : (
        <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
          {items.map((item) => <li key={item}>{item}</li>)}
        </ul>
      )}
    </div>
  );
}

function formatOperationLabel(value: string): string {
  return value.replaceAll('_', ' ');
}

function UpgradePlan({ plan }: { plan: AgentUpgradePlanResponse }) {
  return (
    <div className="mt-4 rounded-md border border-border bg-background p-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-medium text-muted-foreground">Target</span>
        <span className="font-mono text-xs text-foreground">{plan.targetImage}</span>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-3">
        <MiniStat label="Batch" value={`${plan.batchSize || 1}`} />
        <MiniStat label="Max unavailable" value={`${plan.maxUnavailable || 1}`} />
        <MiniStat label="Canaries" value={`${plan.canaryClusterIds?.length ?? 0}`} />
      </div>
      {plan.rollbackImage && (
        <div className="mt-3 flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2">
          <span className="text-xs font-medium text-muted-foreground">Rollback</span>
          <span className="truncate font-mono text-xs text-foreground">{plan.rollbackImage}</span>
        </div>
      )}
      {!plan.ready && plan.blockers?.length ? (
        <div className="mt-3 rounded-md border border-status-warning/20 bg-status-warning/10 px-3 py-2">
          <p className="text-xs font-medium text-status-warning">Blocked</p>
          <ul className="mt-1 space-y-1 text-xs text-status-warning">
            {plan.blockers.map((blocker) => <li key={blocker}>{blocker}</li>)}
          </ul>
        </div>
      ) : (
        <p className="mt-3 text-xs text-status-success">Ready for manifest rollout</p>
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
        {items.map((item) => <li key={item}>{item}</li>)}
      </ol>
    </div>
  );
}
