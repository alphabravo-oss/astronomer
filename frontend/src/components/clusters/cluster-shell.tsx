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
import { Loader2, Terminal as TerminalIcon, RefreshCw, AlertCircle, Clock, Play, Square } from 'lucide-react';
import { Terminal, useTerminal } from '@wterm/react';
import '@wterm/react/css';
import {
  openShellSession,
  closeShellSession,
  listShellSessionCommands,
  type ShellSession,
  type RecordedCommand,
} from '@/lib/api/kubectl-shell';
import { createStreamTicket } from '@/lib/api';
import { wsBase } from '@/lib/env';
import { cn } from '@/lib/utils';
import { StatusBadge as UiStatusBadge } from '@/components/ui/status-badge';

type Status = 'idle' | 'opening' | 'connecting' | 'connected' | 'disconnected' | 'error';

interface ClusterShellProps {
  clusterId: string;
}

export function ClusterShell({ clusterId }: ClusterShellProps) {
  const { ref, write } = useTerminal();
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

  // Explicit Connect handler — fires when the operator clicks "Connect"
  // (previously this ran automatically on mount, which surfaced a flood
  // of WebSocket reconnect attempts whenever auth / network failed and
  // gave operators no way to bail out before a session was provisioned
  // on the cluster).
  const handleConnect = useCallback(async () => {
    if (status === 'opening' || status === 'connecting' || status === 'connected') {
      return; // already in-flight
    }
    setStatus('opening');
    setErrorMsg('');
    try {
      const info = await openShellSession(clusterId);
      setSession(info);
      sessionRef.current = info;
      // If wterm's onReady already fired before this resolves, kick the
      // WebSocket immediately; otherwise handleReady will pick it up
      // when the core is up.
      if (readyRef.current) {
        connectWS(info);
      }
      setStatus('connecting');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setStatus('error');
      setErrorMsg(msg);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, status]);

  // Explicit Disconnect handler — closes the WS and tears down the
  // pod-side session. Best-effort; reaper will catch any miss.
  const handleDisconnect = useCallback(async () => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      try { ws.close(1000, 'client requested disconnect'); } catch { /* ignore */ }
    }
    wsRef.current = null;
    const s = sessionRef.current;
    sessionRef.current = null;
    setStatus('disconnected');
    if (s) {
      await closeShellSession(clusterId, s.id).catch(() => {});
    }
  }, [clusterId]);

  // Unmount cleanup: if a session is still live, close it server-side
  // so the cluster-side pod is reaped immediately instead of waiting
  // for idleTimeout. Same fire-and-forget semantics as Disconnect.
  useEffect(() => {
    return () => {
      try { wsRef.current?.close(); } catch { /* ignore */ }
      if (sessionRef.current) {
        closeShellSession(clusterId, sessionRef.current.id).catch(() => {});
      }
    };
  }, [clusterId]);

  const connectWS = useCallback((info: ShellSession) => {
    createStreamTicket('shell', info.clusterId)
      .then(({ ticket }) => {
        const ticketQuery = `?ticket=${encodeURIComponent(ticket)}`;
        const wsUrl = `${wsBase()}/clusters/${info.clusterId}/shell/sessions/${info.id}/${ticketQuery}`;
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
      })
      .catch((error: Error) => {
        setStatus('error');
        setErrorMsg(error.message || 'Failed to create stream ticket');
      });
  }, [write]);

  // Fires once wterm's WASM core is initialized and ready to accept writes.
  const handleReady = useCallback(() => {
    readyRef.current = true;
    write(`\x1b[36mOpening shell on cluster ${clusterId}\x1b[0m\r\n`);
    if (sessionRef.current) {
      const info = sessionRef.current;
      write(`\x1b[2mpod: ${info.podName} (${info.podNamespace})  container: ${info.container}\x1b[0m\r\n\r\n`);
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
  //
  // Critical: do NOT call resize(cols, rows) here. The handler runs
  // *after* the terminal has already resized to (cols, rows), so calling
  // resize() back into the terminal triggers another onResize, recursing
  // until the JS stack blows up ("Maximum call stack size exceeded" at
  // _isScrolledToBottom → resize → onResize). Just forward to the WS.
  const handleResize = useCallback((cols: number, rows: number) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols, rows }));
    }
  }, []);

  // Age + countdown copy.
  const sessionAgeSeconds = session ? Math.floor((now - new Date(session.startedAt).getTime()) / 1000) : 0;
  const lastInputSeconds = session ? Math.floor((now - new Date(session.lastInputAt).getTime()) / 1000) : 0;
  const expiresInSeconds = session ? Math.max(0, Math.floor((new Date(session.expiresAt).getTime() - now) / 1000)) : 0;
  const idleExpiresInSeconds = session
    ? Math.max(0, session.idleTimeoutSeconds - lastInputSeconds)
    : 0;

  // isLive == "a WebSocket is open or about to be"; opening is its own
  // pre-WS state (POST /shell/sessions/ in flight) so the button can
  // render a spinner without making Disconnect available before the
  // session actually exists.
  const isLive = status === 'connecting' || status === 'connected';
  const isOpening = status === 'opening';

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between p-2 border-b bg-background gap-3">
        <div className="flex items-center gap-2 text-sm">
          <TerminalIcon className="h-4 w-4" />
          <span className="font-medium">Cluster shell</span>
          <ShellStatusBadge status={status} />
        </div>
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {session && isLive ? (
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
          ) : isOpening ? (
            <span>Preparing session...</span>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {isLive ? (
            <button
              onClick={handleDisconnect}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium border border-border bg-background hover:bg-muted text-red-600"
              title="Close the WebSocket and tear down the in-cluster debug pod"
            >
              <Square className="h-3 w-3" />
              Disconnect
            </button>
          ) : isOpening ? (
            <button
              disabled
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-primary text-primary-foreground opacity-60"
            >
              <Loader2 className="h-3 w-3 animate-spin" />
              Opening…
            </button>
          ) : (
            <button
              onClick={handleConnect}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-primary text-primary-foreground hover:opacity-90"
              title="Provision an ephemeral debug pod and open a shell"
            >
              <Play className="h-3 w-3" />
              {status === 'disconnected' || status === 'error' ? 'Reconnect' : 'Connect'}
            </button>
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
              Preparing ephemeral debug pod...
            </div>
          )}
          {status === 'idle' && (
            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div className="text-center max-w-sm pointer-events-auto">
                <TerminalIcon className="h-8 w-8 mx-auto text-muted-foreground mb-3" />
                <p className="text-sm font-medium text-foreground">No active session</p>
                <p className="text-xs text-muted-foreground mt-1.5 mb-4">
                  Clicking <strong>Connect</strong> spins up an ephemeral kubectl pod in
                  <code className="mx-1 px-1 rounded bg-muted font-mono">kube-system</code>, opens a shell into it,
                  and records every command line you type to the audit log.
                </p>
                <button
                  onClick={handleConnect}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-primary text-primary-foreground hover:opacity-90"
                >
                  <Play className="h-3 w-3" />
                  Connect
                </button>
              </div>
            </div>
          )}
          {(status === 'disconnected' || status === 'error') && (
            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div className="text-center max-w-sm pointer-events-auto">
                <AlertCircle className={cn('h-8 w-8 mx-auto mb-3', status === 'error' ? 'text-red-500' : 'text-amber-500')} />
                <p className="text-sm font-medium text-foreground">
                  {status === 'error' ? 'Connection failed' : 'Session disconnected'}
                </p>
                {errorMsg && (
                  <p className="text-xs text-muted-foreground mt-1.5 font-mono break-words">
                    {errorMsg}
                  </p>
                )}
                <button
                  onClick={handleConnect}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 mt-4 rounded-md text-xs font-medium bg-primary text-primary-foreground hover:opacity-90"
                >
                  <Play className="h-3 w-3" />
                  Reconnect
                </button>
              </div>
            </div>
          )}
          {/* wterm renders an off-screen helper textarea that grabs
              focus for IME / paste. When the terminal is hidden behind
              an empty-state we set inert to keep that textarea out of
              the a11y tree (xterm marks it aria-hidden=true but Chrome
              still warns when focus lands inside, because aria-hidden
              on a focused descendant violates WAI-ARIA). inert prevents
              focus entirely, which is the spec-recommended fix. */}
          <div
            className={cn('h-full w-full', !(isLive || isOpening) && 'pointer-events-none opacity-0')}
            {...(!(isLive || isOpening) ? { inert: '' as unknown as undefined } : {})}
          >
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
                    {new Date(c.commandAt).toLocaleTimeString()}{' '}
                  </span>
                  <span className={cn('text-foreground')}>{c.commandLine}</span>
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

function ShellStatusBadge({ status }: { status: Status }) {
  const badgeStatus = status === 'opening' ? 'connecting' : status === 'idle' ? 'disconnected' : status;
  const label = status === 'idle' ? 'not connected' : status;
  return <UiStatusBadge status={badgeStatus} label={label} size="sm" />;
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
