'use client';

// Migration 065 / sprint 17 — in-browser kubectl shell.
//
// Renders a full-page xterm.js terminal wired to a kubectl_sessions row.
// On mount: POST to /shell/sessions/, open WebSocket, stream stdin/stdout.
// On unmount: POST close (best-effort) so the in-cluster pod is torn down.
//
// Status bar shows session age, time since last input, and time until
// idle expiry. A second pane shows the operator's own recorded
// command lines (server-side audit log).

import { useEffect, useRef, useState, useCallback } from 'react';
import { Loader2, Terminal as TerminalIcon, RefreshCw, AlertCircle, Clock } from 'lucide-react';
import { useTheme } from 'next-themes';
import {
  openShellSession,
  closeShellSession,
  listShellSessionCommands,
  type ShellSession,
  type RecordedCommand,
} from '@/lib/api/kubectl-shell';
import { cn } from '@/lib/utils';
import '@xterm/xterm/css/xterm.css';

type Status = 'idle' | 'opening' | 'connecting' | 'connected' | 'disconnected' | 'error';

interface ClusterShellProps {
  clusterId: string;
}

export function ClusterShell({ clusterId }: ClusterShellProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<import('@xterm/xterm').Terminal | null>(null);
  const fitRef = useRef<import('@xterm/addon-fit').FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const sessionRef = useRef<ShellSession | null>(null);

  const { theme } = useTheme();
  const [status, setStatus] = useState<Status>('idle');
  const [errorMsg, setErrorMsg] = useState<string>('');
  const [session, setSession] = useState<ShellSession | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [commands, setCommands] = useState<RecordedCommand[]>([]);

  // 1s ticker drives the "session age" + "auto-expires in" status-bar copy.
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  // Periodically refresh the recorded-commands pane (every 5s).
  useEffect(() => {
    if (!session || status !== 'connected') return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const rows = await listShellSessionCommands(clusterId, session.id);
        if (!cancelled) setCommands(rows);
      } catch {
        // Ignore; pane will re-fetch on the next tick.
      }
    };
    refresh();
    const t = setInterval(refresh, 5000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [clusterId, session, status]);

  // Open the session + bring up xterm on mount.
  useEffect(() => {
    let cancelled = false;
    let term: import('@xterm/xterm').Terminal | null = null;
    let fitAddon: import('@xterm/addon-fit').FitAddon | null = null;

    const boot = async () => {
      try {
        setStatus('opening');
        setErrorMsg('');
        const info = await openShellSession(clusterId);
        if (cancelled) {
          // Component unmounted before the POST completed — best-effort close.
          await closeShellSession(clusterId, info.id).catch(() => {});
          return;
        }
        setSession(info);
        sessionRef.current = info;

        // Lazy-import xterm so the bundle splits cleanly.
        const { Terminal } = await import('@xterm/xterm');
        const { FitAddon } = await import('@xterm/addon-fit');
        const { WebLinksAddon } = await import('@xterm/addon-web-links');
        if (cancelled || !terminalRef.current) return;

        term = new Terminal({
          cursorBlink: true,
          fontSize: 13,
          fontFamily: 'var(--font-mono), "JetBrains Mono", "Fira Code", monospace',
          theme: themeColors(theme),
          allowProposedApi: true,
          scrollback: 5000,
          convertEol: true,
        });
        fitAddon = new FitAddon();
        term.loadAddon(fitAddon);
        term.loadAddon(new WebLinksAddon());
        term.open(terminalRef.current);
        fitAddon.fit();
        termRef.current = term;
        fitRef.current = fitAddon;

        term.write(`\x1b[36mOpening shell on cluster ${clusterId}\x1b[0m\r\n`);
        term.write(`\x1b[2mpod: ${info.pod_name} (${info.pod_namespace})  container: ${info.container}\x1b[0m\r\n\r\n`);

        setStatus('connecting');
        const ws = openWebSocket(info, term);
        wsRef.current = ws;
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err);
        setStatus('error');
        setErrorMsg(msg);
      }
    };

    boot();

    return () => {
      cancelled = true;
      try { wsRef.current?.close(); } catch {/* ignore */}
      try { term?.dispose(); } catch {/* ignore */}
      termRef.current = null;
      fitRef.current = null;
      // Tear down the in-cluster pod via the close endpoint. Fire-and-forget;
      // the reaper will catch any miss.
      if (sessionRef.current) {
        closeShellSession(clusterId, sessionRef.current.id).catch(() => {});
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId]);

  // Window-resize → xterm fit.
  useEffect(() => {
    const onResize = () => fitRef.current?.fit();
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  const openWebSocket = useCallback((info: ShellSession, term: import('@xterm/xterm').Terminal) => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const apiBase = (process.env.NEXT_PUBLIC_API_URL || '/api/v1').replace(/\/$/, '');
    const wsHost = apiBase.startsWith('/') ? `${proto}//${window.location.host}${apiBase}` : apiBase.replace(/^https?:/, proto);
    const wsHostNoTrail = wsHost.replace(/\/$/, '');
    const token = typeof window !== 'undefined' ? localStorage.getItem('astronomer_token') : null;
    const tokenQuery = token ? `?token=${encodeURIComponent(token)}` : '';
    const wsUrl = `${wsHostNoTrail}/ws/clusters/${info.cluster_id}/shell/sessions/${info.id}/${tokenQuery}`;

    const ws = new WebSocket(wsUrl);
    ws.onopen = () => {
      setStatus('connected');
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
    };
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data?.type === 'output' || data?.type === 'stdout' || data?.type === 'stderr') {
          term.write(data.data ?? '');
          return;
        }
        if (data?.type === 'error') {
          term.write(`\r\n\x1b[31mError: ${data.message ?? 'unknown'}\x1b[0m\r\n`);
          return;
        }
        if (data?.type === 'end') {
          term.write(`\r\n\x1b[33mSession ended${data.reason ? `: ${data.reason}` : ''}\x1b[0m\r\n`);
          return;
        }
        if (typeof data?.data === 'string') term.write(data.data);
      } catch {
        term.write(event.data);
      }
    };
    ws.onerror = () => {
      setStatus('error');
      setErrorMsg('WebSocket error');
    };
    ws.onclose = () => {
      setStatus('disconnected');
      term.write('\r\n\x1b[33mConnection closed\x1b[0m\r\n');
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'stdin', data }));
      }
    });
    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });
    return ws;
  }, []);

  // Age + countdown copy.
  const sessionAgeSeconds = session ? Math.floor((now - new Date(session.started_at).getTime()) / 1000) : 0;
  const lastInputSeconds = session ? Math.floor((now - new Date(session.last_input_at).getTime()) / 1000) : 0;
  const expiresInSeconds = session ? Math.max(0, Math.floor((new Date(session.expires_at).getTime() - now) / 1000)) : 0;
  const idleExpiresInSeconds = session
    ? Math.max(0, session.idle_timeout_seconds - lastInputSeconds)
    : 0;

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between p-2 border-b bg-background">
        <div className="flex items-center gap-2 text-sm">
          <TerminalIcon className="h-4 w-4" />
          <span className="font-medium">Cluster shell</span>
          <StatusBadge status={status} />
        </div>
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {session ? (
            <>
              <span>Session age: {formatDuration(sessionAgeSeconds)}</span>
              <span>•</span>
              <span>Last input: {formatDuration(lastInputSeconds)} ago</span>
              <span>•</span>
              <span className="flex items-center gap-1">
                <Clock className="h-3 w-3" />
                Auto-expires in {formatDuration(Math.min(expiresInSeconds, idleExpiresInSeconds))}
              </span>
            </>
          ) : (
            <span>Preparing session...</span>
          )}
        </div>
      </div>

      {errorMsg ? (
        <div className="m-3 rounded border border-red-300 bg-red-50 dark:bg-red-950/30 p-3 text-sm flex items-start gap-2">
          <AlertCircle className="h-4 w-4 text-red-600 mt-0.5 shrink-0" />
          <div>
            <div className="font-medium">Failed to open shell</div>
            <div className="text-muted-foreground">{errorMsg}</div>
          </div>
        </div>
      ) : null}

      <div className="flex-1 flex min-h-0">
        <div className="flex-1 bg-black min-h-0">
          {status === 'opening' && (
            <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
              Provisioning ephemeral debug pod...
            </div>
          )}
          <div ref={terminalRef} className="h-full w-full" />
        </div>
        <div className="w-72 border-l bg-background overflow-y-auto p-3">
          <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground mb-2">
            <RefreshCw className="h-3 w-3" />
            Recorded commands ({commands.length})
          </div>
          {commands.length === 0 ? (
            <p className="text-xs text-muted-foreground italic">No commands recorded yet.</p>
          ) : (
            <ul className="space-y-1 text-xs font-mono">
              {commands.slice().reverse().map((c, i) => (
                <li key={i} className="break-all">
                  <span className="text-muted-foreground">
                    {new Date(c.command_at).toLocaleTimeString()}{' '}
                  </span>
                  <span className={cn('text-foreground')}>{c.command_line}</span>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-3 text-[10px] text-muted-foreground">
            Only your input lines are recorded — never output. See
            docs/kubectl-shell.md for the audit-log contract.
          </p>
        </div>
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: Status }) {
  const label =
    status === 'connected' ? 'connected' :
    status === 'connecting' ? 'connecting' :
    status === 'opening' ? 'opening' :
    status === 'disconnected' ? 'disconnected' :
    status === 'error' ? 'error' : 'idle';
  const colour =
    status === 'connected' ? 'bg-green-500' :
    status === 'error' ? 'bg-red-500' :
    status === 'disconnected' ? 'bg-yellow-500' :
    'bg-blue-500';
  return (
    <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
      <span className={cn('h-2 w-2 rounded-full', colour)} />
      {label}
    </span>
  );
}

function formatDuration(secs: number): string {
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  if (m < 60) return `${m}m${s ? ` ${s}s` : ''}`;
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return `${h}h${remM ? ` ${remM}m` : ''}`;
}

function themeColors(theme: string | undefined) {
  if (theme === 'dark') {
    return {
      background: '#0a0a0a',
      foreground: '#e5e5e5',
      cursor: '#22d3ee',
      cursorAccent: '#0a0a0a',
      selectionBackground: '#374151',
    };
  }
  return {
    background: '#0a0a0a',
    foreground: '#e5e5e5',
    cursor: '#22d3ee',
    cursorAccent: '#0a0a0a',
    selectionBackground: '#374151',
  };
}
