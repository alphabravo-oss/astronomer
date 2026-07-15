'use client';

// PodTerminal — exec-into-pod terminal pane.
//
// Terminal backend: wterm (@wterm/react) — Zig→WASM core with DOM-native
// selection / copy-paste / accessibility. Migrated from xterm.js
// 2026-05-12.

import { useEffect, useRef, useState, useCallback } from 'react';
import { useTheme } from 'next-themes';
import { Terminal as TerminalIcon, RefreshCw, X, ChevronDown } from 'lucide-react';
import { Terminal, useTerminal } from '@wterm/react';
import '@wterm/react/css';
import { cn } from '@/lib/utils';
import { createStreamTicket } from '@/lib/api';
import { wsBase } from '@/lib/env';

export type TerminalConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

// PodTerminalActions is a tiny imperative API the host can call. Used by
// the window-manager exec tab to focus the terminal when its tab becomes
// active and to wire a toolbar Clear button.
export interface PodTerminalActions {
  focus: () => void;
  clear: () => void;
  fit: () => void;
}

interface PodTerminalProps {
  clusterId: string;
  namespace: string;
  pod: string;
  container: string;
  containers?: string[];
  onClose?: () => void;
  onStatusChange?: (status: TerminalConnectionStatus) => void;
  embedded?: boolean;
  actionsRef?: React.MutableRefObject<PodTerminalActions | null>;
}

type ConnectionStatus = TerminalConnectionStatus;

export function PodTerminal({
  clusterId,
  namespace,
  pod,
  container: initialContainer,
  containers = [],
  onClose,
  onStatusChange,
  embedded = false,
  actionsRef,
}: PodTerminalProps) {
  const { ref, write, resize, focus } = useTerminal();
  const wsRef = useRef<WebSocket | null>(null);
  // Tracks the in-flight connect attempt so a cleanup (unmount, container
  // switch, StrictMode double-invoke) that runs *before* the stream-ticket XHR
  // resolves can cancel it. Without this, cleanup sees wsRef.current === null,
  // closes nothing, and the pending .then later opens an orphaned WebSocket on
  // an unmounted component — leaking a server-side exec stream (SPDY session +
  // one of the shared per-agent slots) until the browser GCs it.
  const connectAttemptRef = useRef<{ cancelled: boolean } | null>(null);
  // Latest cols/rows wterm has reported via onResize — used to send the
  // initial TIOCSWINSZ on WS open.
  const sizeRef = useRef<{ cols: number; rows: number }>({ cols: 80, rows: 24 });
  // Use theme for re-renders only; wterm themes are name-based, not RGB
  // objects, so we let the WASM core handle palette switching itself.
  useTheme();

  const [selectedContainer, setSelectedContainer] = useState(initialContainer);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');
  // Flipped once the wterm WASM core is up. Gates the connect effect so we
  // don't open a socket before the terminal can render its output.
  const [ready, setReady] = useState(false);
  const [showContainerDropdown, setShowContainerDropdown] = useState(false);
  const containerDropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    onStatusChange?.(status);
  }, [status, onStatusChange]);

  const connectWebSocket = useCallback(() => {
    const wsHost = wsBase();

    const attempt = { cancelled: false };
    connectAttemptRef.current = attempt;

    setStatus('connecting');
    createStreamTicket('exec', clusterId)
      .then(({ ticket }) => {
        // Cleanup already ran while the ticket was in flight — don't open a
        // socket that nothing will ever close.
        if (attempt.cancelled) return;
        const ticketQuery = `?ticket=${encodeURIComponent(ticket)}`;
        const wsUrl = `${wsHost}/exec/${clusterId}/${namespace}/${pod}/${selectedContainer}/${ticketQuery}`;
        const ws = new WebSocket(wsUrl);
        wsRef.current = ws;

        ws.onopen = () => {
          setStatus('connected');
          ws.send(JSON.stringify({ type: 'resize', cols: sizeRef.current.cols, rows: sizeRef.current.rows }));
        };

        ws.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data);
            if (data && typeof data === 'object') {
              if (data.type === 'output' || data.type === 'stdout' || data.type === 'stderr') {
                write(data.data ?? '');
                return;
              }
              if (data.type === 'error') {
                write(`\r\n\x1b[31mError: ${data.message ?? 'unknown error'}\x1b[0m\r\n`);
                return;
              }
              if (data.type === 'end') {
                write(`\r\n\x1b[33mSession ended${data.reason ? `: ${data.reason}` : ''}\x1b[0m\r\n`);
                return;
              }
              if (typeof data.data === 'string') {
                write(data.data);
                return;
              }
            }
            if (typeof data === 'string') {
              write(data);
            }
          } catch {
            write(event.data);
          }
        };

        ws.onerror = () => {
          setStatus('error');
          write('\r\n\x1b[31mWebSocket connection error\x1b[0m\r\n');
        };

        ws.onclose = (event) => {
          setStatus('disconnected');
          const reason = event.reason || (event.code === 1006 ? 'connection lost' : '');
          write(`\r\n\x1b[33mConnection closed${reason ? `: ${reason}` : ''}\x1b[0m\r\n`);
          write('\x1b[33mPress the reconnect button to try again\x1b[0m\r\n');
        };
      })
      .catch((error: Error) => {
        if (attempt.cancelled) return;
        setStatus('error');
        write(`\r\n\x1b[31mFailed to create stream ticket: ${error.message}\x1b[0m\r\n`);
      });
  }, [clusterId, namespace, pod, selectedContainer, write]);

  // Fires once the wterm WASM core is up. The actual WS connect is driven by
  // the effect below (gated on `ready`) so that switching containers can
  // re-run it; here we just wire the imperative actions and mark ready.
  const handleReady = useCallback(() => {
    write(`Connecting to \x1b[36m${pod}\x1b[0m / \x1b[33m${selectedContainer}\x1b[0m ...\r\n`);
    if (actionsRef) {
      actionsRef.current = {
        focus,
        clear: () => write('\x1b[2J\x1b[H'),
        fit: () => { /* wterm autoResize handles fit; no-op */ },
      };
    }
    if (embedded) focus();
    setReady(true);
  }, [pod, selectedContainer, write, focus, embedded, actionsRef]);

  // (Re)connect whenever the selected container changes, once the core is
  // ready. connectWebSocket's identity tracks selectedContainer, so picking a
  // new container from the dropdown tears the old socket down (cleanup) and
  // opens a fresh session for the new container instead of leaving the
  // terminal stuck on 'disconnected'.
  useEffect(() => {
    if (!ready) return;
    connectWebSocket();
    return () => {
      // Cancel any still-pending ticket so its .then won't open an orphan
      // socket after we've torn down.
      if (connectAttemptRef.current) connectAttemptRef.current.cancelled = true;
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [ready, connectWebSocket]);

  // Operator keystrokes → ws stdin.
  const handleData = useCallback((data: string) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'stdin', data }));
    }
  }, []);

  const handleResize = useCallback((cols: number, rows: number) => {
    sizeRef.current = { cols, rows };
    resize(cols, rows);
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols, rows }));
    }
  }, [resize]);

  // Clear the imperative actions handle on unmount. (WS teardown is handled
  // by the connect effect above.)
  useEffect(() => {
    return () => {
      if (actionsRef) actionsRef.current = null;
    };
  }, [actionsRef]);

  // Close container dropdown on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (containerDropdownRef.current && !containerDropdownRef.current.contains(e.target as Node)) {
        setShowContainerDropdown(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  const handleReconnect = () => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    write('\x1b[2J\x1b[H');
    write(`Reconnecting to \x1b[36m${pod}\x1b[0m / \x1b[33m${selectedContainer}\x1b[0m ...\r\n`);
    connectWebSocket();
  };

  const handleContainerChange = (containerName: string) => {
    setSelectedContainer(containerName);
    setShowContainerDropdown(false);
  };

  const statusColors: Record<ConnectionStatus, string> = {
    connecting: 'bg-status-warning',
    connected: 'bg-status-success',
    disconnected: 'bg-status-neutral',
    error: 'bg-status-error',
  };

  const statusLabels: Record<ConnectionStatus, string> = {
    connecting: 'Connecting...',
    connected: 'Connected',
    disconnected: 'Disconnected',
    error: 'Error',
  };

  return (
    <div
      className={cn(
        'flex flex-col h-full overflow-hidden bg-background',
        embedded ? '' : 'rounded-lg border border-border'
      )}
    >
      {!embedded && (
      <div className="flex items-center justify-between px-3 py-2 border-b border-border bg-muted/50">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <TerminalIcon className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-foreground">Terminal</span>
          </div>

          <div className="flex items-center gap-1.5">
            <span className={cn('h-2 w-2 rounded-full', statusColors[status])} />
            <span className="text-xs text-muted-foreground">{statusLabels[status]}</span>
          </div>

          {containers.length > 1 && (
            <div ref={containerDropdownRef} className="relative">
              <button
                onClick={() => setShowContainerDropdown(!showContainerDropdown)}
                className="inline-flex items-center gap-1.5 h-6 px-2 rounded border border-border text-xs
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                <span className="font-mono">{selectedContainer}</span>
                <ChevronDown className="h-3 w-3" />
              </button>

              {showContainerDropdown && (
                <div className="absolute left-0 top-full mt-1 w-48 rounded-md border border-border bg-popover p-1 shadow-lg z-50">
                  {containers.map((c) => (
                    <button
                      key={c}
                      onClick={() => handleContainerChange(c)}
                      className={cn(
                        'w-full flex items-center px-2.5 py-1.5 rounded text-xs text-left transition-colors font-mono',
                        c === selectedContainer
                          ? 'bg-accent text-foreground'
                          : 'text-muted-foreground hover:text-foreground hover:bg-accent'
                      )}
                    >
                      {c}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center gap-1">
          <button
            onClick={handleReconnect}
            className="inline-flex items-center gap-1 h-6 px-2 rounded text-xs
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Reconnect"
          >
            <RefreshCw className="h-3 w-3" />
            Reconnect
          </button>

          {onClose && (
            <button
              onClick={onClose}
              className="inline-flex items-center justify-center h-6 w-6 rounded
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              title="Close terminal"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>
      )}

      <div className="flex-1 min-h-0 p-1">
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
  );
}
