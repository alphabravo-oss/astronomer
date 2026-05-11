'use client';

import { useEffect, useRef, useState } from 'react';
import { PodTerminal, type PodTerminalActions, type TerminalConnectionStatus } from '@/components/workloads/pod-terminal';
import { Eraser, RefreshCw } from 'lucide-react';
import { cn } from '@/lib/utils';

interface ExecTabProps {
  clusterId: string;
  namespace: string;
  pod: string;
  container?: string;
  visible: boolean;
  onStatusChange?: (status: TerminalConnectionStatus) => void;
}

// Tab body for an exec terminal living inside the WindowManager. Keeps the
// underlying PodTerminal mounted (just visually hidden) when the tab isn't
// active so the WS session and xterm buffer survive tab switches.
export function ExecTab({
  clusterId,
  namespace,
  pod,
  container,
  visible,
  onStatusChange,
}: ExecTabProps) {
  const [status, setStatus] = useState<TerminalConnectionStatus>('connecting');
  // Bumping this remounts PodTerminal, which reliably reopens its WS — far
  // simpler than imperatively exposing reconnect() through a ref.
  const [reconnectNonce, setReconnectNonce] = useState(0);
  const termActionsRef = useRef<PodTerminalActions | null>(null);

  useEffect(() => {
    onStatusChange?.(status);
  }, [status, onStatusChange]);

  // Focus the xterm whenever this tab becomes the active one — so the user
  // can start typing immediately without an extra click. Also re-fit so the
  // hidden→visible transition picks up the real container dimensions.
  useEffect(() => {
    if (!visible) return;
    const id = requestAnimationFrame(() => {
      termActionsRef.current?.fit();
      termActionsRef.current?.focus();
    });
    return () => cancelAnimationFrame(id);
  }, [visible, reconnectNonce]);

  return (
    <div
      className="flex flex-col h-full bg-background"
      style={{ display: visible ? 'flex' : 'none' }}
    >
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2 px-3 py-1.5 bg-muted/40 border-b border-border">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono truncate max-w-[280px]" title={`${namespace}/${pod}`}>
            {namespace}/{pod}
          </span>
          {container && (
            <span className="font-mono text-foreground/80">· {container}</span>
          )}
          <div className="flex items-center gap-1.5 ml-2">
            <span className={cn('h-2 w-2 rounded-full', statusPillBg(status))} />
            <span className="text-2xs">{statusLabel(status)}</span>
          </div>
        </div>

        <div className="flex items-center gap-1">
          <button
            onClick={() => {
              termActionsRef.current?.clear();
              // Hand focus back to xterm so the user can keep typing — the
              // button itself stole focus on click and a stray keystroke
              // would otherwise miss the shell.
              termActionsRef.current?.focus();
            }}
            disabled={status !== 'connected'}
            className="inline-flex items-center gap-1 h-6 px-2 rounded text-2xs
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors
              disabled:opacity-40 disabled:cursor-not-allowed"
            title="Clear terminal"
          >
            <Eraser className="h-3 w-3" />
            <span className="hidden sm:inline">Clear</span>
          </button>
          {(status === 'disconnected' || status === 'error') && (
            <button
              onClick={() => setReconnectNonce((n) => n + 1)}
              className="inline-flex items-center gap-1 h-6 px-2 rounded text-2xs
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              title="Reconnect"
            >
              <RefreshCw className="h-3 w-3" />
              Reconnect
            </button>
          )}
        </div>
      </div>

      {/* Terminal body */}
      <div className="flex-1 min-h-0">
        <PodTerminal
          key={reconnectNonce}
          clusterId={clusterId}
          namespace={namespace}
          pod={pod}
          container={container || ''}
          embedded
          onStatusChange={setStatus}
          actionsRef={termActionsRef}
        />
      </div>
    </div>
  );
}

function statusPillBg(s: TerminalConnectionStatus): string {
  switch (s) {
    case 'connected':
      return 'bg-status-success';
    case 'connecting':
      return 'bg-status-warning';
    case 'error':
      return 'bg-status-error';
    case 'disconnected':
    default:
      return 'bg-status-neutral';
  }
}

function statusLabel(s: TerminalConnectionStatus): string {
  switch (s) {
    case 'connected':
      return 'Connected';
    case 'connecting':
      return 'Connecting...';
    case 'error':
      return 'Error';
    case 'disconnected':
    default:
      return 'Disconnected';
  }
}
