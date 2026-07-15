import { createFileRoute } from '@tanstack/react-router';

/**
 * Shell sessions audit view (F-05). Superuser-only surface listing every
 * active kubectl shell session across the fleet, with drill-down to the
 * per-session audited command trail — closing the loop on the kubectl-shell
 * RCE surface. Wired to GET /admin/shell-sessions[/{id}/commands].
 */

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from '@/lib/link';
import { ArrowLeft, TerminalSquare, X, Loader2 } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { EmptyState } from '@/components/ui/empty-state';
import {
  listShellSessions,
  listShellSessionCommands,
  type ShellSession,
} from '@/lib/api';
import { queryKeys } from '@/lib/query-keys';
import { formatDate, formatRelativeTime } from '@/lib/utils';

function ShellSessionsPage() {
  const [selected, setSelected] = useState<ShellSession | null>(null);

  const { data: sessions = [], isLoading, isError, refetch } = useQuery({
    queryKey: queryKeys.adminSecurity.shellSessions,
    queryFn: listShellSessions,
    staleTime: 10_000,
  });

  const columns: Column<ShellSession>[] = [
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => <span className="font-mono text-xs text-foreground">{row.clusterId}</span>,
      sortAccessor: (row) => row.clusterId,
    },
    {
      key: 'user',
      header: 'User',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.userId}</span>,
      sortAccessor: (row) => row.userId,
    },
    {
      key: 'pod',
      header: 'Pod',
      accessor: (row) => (
        <span className="text-sm text-foreground">
          {row.podNamespace}/{row.podName}
          {row.container ? <span className="text-muted-foreground"> · {row.container}</span> : null}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">{row.status}</span>
      ),
    },
    {
      key: 'commands',
      header: 'Commands',
      accessor: (row) => <span className="tabular-nums text-sm">{row.commandCount ?? 0}</span>,
      sortAccessor: (row) => row.commandCount ?? 0,
      align: 'center',
    },
    {
      key: 'started',
      header: 'Started',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.startedAt)}</span>,
      sortAccessor: (row) => row.startedAt,
    },
  ];

  return (
    <div className="space-y-6">
      <div>
        <Link
          href="/dashboard/audit"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Audit Log
        </Link>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-2 flex items-center gap-2">
          <TerminalSquare className="h-6 w-6" />
          Shell Sessions
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Active kubectl shell sessions across every cluster. Click a session to see its command trail.
        </p>
      </div>

      <DataTable
        data={sessions}
        columns={columns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Filter sessions..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        onRowClick={(row) => setSelected(row)}
        emptyMessage="No active shell sessions"
      />

      {selected && (
        <SessionCommandsDrawer session={selected} onClose={() => setSelected(null)} />
      )}
    </div>
  );
}

function SessionCommandsDrawer({
  session,
  onClose,
}: {
  session: ShellSession;
  onClose: () => void;
}) {
  const { data: commands = [], isLoading } = useQuery({
    queryKey: queryKeys.adminSecurity.shellSessionCommands(session.id),
    queryFn: () => listShellSessionCommands(session.id),
  });

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-2xl max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div>
            <h3 className="text-lg font-semibold text-foreground">Command trail</h3>
            <p className="text-xs text-muted-foreground font-mono mt-0.5">
              {session.podNamespace}/{session.podName} · started {formatDate(session.startedAt)}
            </p>
          </div>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6">
          {isLoading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground py-6">
              <Loader2 className="h-4 w-4 animate-spin" />
              Loading commands…
            </div>
          ) : commands.length === 0 ? (
            <EmptyState
              icon={TerminalSquare}
              title="No commands recorded"
              description="This session has not executed any audited commands yet."
            />
          ) : (
            <ol className="space-y-1.5 font-mono text-xs">
              {commands.map((cmd, i) => (
                <li key={i} className="flex gap-3 rounded-md bg-muted/40 px-3 py-2">
                  <span className="text-muted-foreground whitespace-nowrap">{formatDate(cmd.commandAt)}</span>
                  <span className="text-foreground break-all">{cmd.commandLine}</span>
                </li>
              ))}
            </ol>
          )}
        </div>
      </div>
    </OverlayShell>
  );
}

export const Route = createFileRoute('/dashboard/audit/shell-sessions/')({
  component: ShellSessionsPage,
});
