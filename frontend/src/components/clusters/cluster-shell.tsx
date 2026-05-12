'use client';

// Migration 065 / sprint 17 — in-browser kubectl shell.
//
// Renders a full-page terminal wired to a kubectl_sessions row.
// On mount: POST to /shell/sessions/, open WebSocket, stream stdin/stdout.
// On unmount: POST close (best-effort) so the in-cluster pod is torn down.
//
// Status bar shows session age, time since last input, and time until
// idle expiry. A second pane shows the operator's own recorded
// command lines (server-side audit log).
//
// Terminal backend: wterm (@wterm/react) — Zig→WASM core with DOM-native
// selection / copy-paste / accessibility. Migrated from xterm.js
// 2026-05-12.

import { useEffect, useRef, useState, useCallback } from 'react';
import { Loader2, Terminal as TerminalIcon, RefreshCw, AlertCircle, Clock } from 'lucide-react';
import { Terminal, useTerminal } from '@wterm/react';
import '@wterm/react/css';
import {
  openShellSession,
  closeShellSession,
  listShellSessionCommands,
  type ShellSession,
  type RecordedCommand,
} from '@/lib/api/kubectl-shell';
import { cn } from '@/lib/utils';

type Status = 'idle' | 'opening' | 'connecting' | 'connected' | 'disconnected' | 'error';

interface ClusterShellProps {
  clusterId: string;
}

export function ClusterShell({ clusterId }: ClusterShellProps) {
  const { ref, write, resize } = useTerminal();
  const wsRef = useRef<WebSocket | null>(null);
  const sessionRef = useRef<ShellSession | null>(null);
  const readyRef = useRef(false);

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

  // POST /shell/sessions/ as soon as we mount. The actual stdin/stdout
  // wiring happens later in `handleReady`, once wterm's WASM core has
  // initialized and we know the terminal can accept writes.
  useEffect(() => {
    let cancelled = false;
    const boot = async () => {
      try {
        setStatus('opening');
        setErrorMsg('');
        const info = await openShellSession(clusterId);
        if (cancelled) {
          await closeShellSession(clusterId, info.id).catch(() => {});
          return;
        }
        setSession(info);
        sessionRef.current = info;
        // If wterm's onReady already fired before this resolves, kick the
        // WebSocket immediately; otherwise handleReady will pick it up
        // when the core is up.
        if (readyRef.current) connectWS(info);
        setStatus('connecting');
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : String(err);
        setStatus('error');
        setErrorMsg(msg);
      }
    };
    boot();
    return () => {
      cancelled = true;
      try { wsRef.current?.close(); } catch { /* ignore */ }
      if (sessionRef.current) {
        // Fire-and-forget — the reaper catches any miss.
        closeShellSession(clusterId, sessionRef.current.id).catch(() => {});
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId]);

  const connectWS = useCallback((info: ShellSession) => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const apiBase = (process.env.NEXT_PUBLIC_API_URL || '/api/v1').replace(/\/$/, '');
    const wsHost = apiBase.startsWith('/') ? `${proto}//${window.location.host}${apiBase}` : apiBase.replace(/^https?:/, proto);
    const wsHostNoTrail = wsHost.replace(/\/$/, '');
    const token = typeof window !== 'undefined' ? localStorage.getItem('astronomer_token') : null;
    const tokenQuery = token ? `?token=${encodeURIComponent(token)}` : '';
    const wsUrl = `${wsHostNoTrail}/ws/clusters/${info.cluster_id}/shell/sessions/${info.id}/${tokenQuery}`;

    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;
    ws.onopen = () => {
      setStatus('connected');
      ws.send(JSON.stringify({ type: 'resize', cols: 80, rows: 24 }));
    };
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data?.type === 'output' || data?.type === 'stdout' || data?.type === 'stderr') {
          write(data.data ?? '');
          return;
        }
        if (data?.type === 'error') {
          write(`\r\n\x1b[31mError: ${data.message ?? 'unknown'}\x1b[0m\r\n`);
          return;
        }
        if (data?.type === 'end') {
          write(`\r\n\x1b[33mSession ended${data.reason ? `: ${data.reason}` : ''}\x1b[0m\r\n`);
          return;
        }
        if (typeof data?.data === 'string') write(data.data);
      } catch {
        write(event.data);
      }
    };
    ws.onerror = () => {
      setStatus('error');
      setErrorMsg('WebSocket error');
    };
    ws.onclose = () => {
      setStatus('disconnected');
      write('\r\n\x1b[33mConnection closed\x1b[0m\r\n');
    };
  }, [write]);

  // Fires once wterm's WASM core is initialized and ready to accept writes.
  const handleReady = useCallback(() => {
    readyRef.current = true;
    write(`\x1b[36mOpening shell on cluster ${clusterId}\x1b[0m\r\n`);
    if (sessionRef.current) {
      const info = sessionRef.current;
      write(`\x1b[2mpod: ${info.pod_name} (${info.pod_namespace})  container: ${info.container}\x1b[0m\r\n\r\n`);
      connectWS(info);
    }
  }, [clusterId, write, connectWS]);

  // Operator keystrokes → ws stdin.
  const handleData = useCallback((data: string) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'stdin', data }));
    }
  }, []);

  // Terminal autoResize → forward to backend so the agent issues TIOCSWINSZ.
  const handleResize = useCallback((cols: number, rows: number) => {
    resize(cols, rows);
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols, rows }));
    }
  }, [resize]);

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
        <div className="flex-1 bg-black min-h-0 relative">
          {status === 'opening' && (
            <div className="absolute top-0 left-0 right-0 flex items-center gap-2 p-4 text-sm text-muted-foreground bg-black/70 z-10">
              <Loader2 className="h-4 w-4 animate-spin" />
              Provisioning ephemeral debug pod...
            </div>
          )}
          <Terminal
            ref={ref}
            cols={80}
            rows={24}
            wasmUrl="/wterm.wasm"
            autoResize
            cursorBlink
            onData={handleData}
            onResize={handleResize}
            onReady={handleReady}
            className="h-full w-full"
          />
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
