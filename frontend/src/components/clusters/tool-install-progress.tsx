'use client';

import { useEffect, useRef } from 'react';
import { useToolOperation } from '@/lib/hooks';
import { CheckCircle2, XCircle, Loader2, ChevronDown, Terminal } from 'lucide-react';

interface ToolInstallProgressProps {
  operationId: string;
  toolName: string;
  onClose: () => void;
}

const TERMINAL = ['completed', 'failed', 'superseded'];

function levelColor(level: string): string {
  if (level === 'error') return 'text-status-error';
  if (level === 'warn' || level === 'warning') return 'text-amber-500';
  return 'text-muted-foreground';
}

// Bottom drawer that tracks a tool install/uninstall operation live — the
// "recent operation" terminal Rancher shows. Streams the operation's events
// (stage + message) until the operation reaches a terminal state.
export function ToolInstallProgress({ operationId, toolName, onClose }: ToolInstallProgressProps) {
  const { data: op } = useToolOperation(operationId);
  const status = op?.status ?? 'pending';
  const isTerminal = TERMINAL.includes(status);
  const failed = status === 'failed';
  const events = op?.events ?? [];

  // Auto-scroll the log to the newest event.
  const logRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [events.length]);

  const StatusIcon = isTerminal ? (failed ? XCircle : CheckCircle2) : Loader2;
  const statusTone = failed ? 'text-status-error' : isTerminal ? 'text-status-active' : 'text-primary';
  const statusLabel = failed
    ? 'Failed'
    : status === 'completed'
      ? 'Deployed'
      : status === 'superseded'
        ? 'Superseded'
        : status === 'running'
          ? 'Installing…'
          : 'Queued…';

  return (
    <div className="fixed bottom-0 inset-x-0 z-50 border-t border-border bg-popover shadow-2xl">
      <div className="mx-auto max-w-5xl">
        <header className="flex items-center justify-between gap-3 px-4 py-2.5 border-b border-border">
          <div className="flex items-center gap-2.5">
            <Terminal className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-foreground">Installing {toolName}</span>
            <span className={`inline-flex items-center gap-1.5 text-xs font-medium ${statusTone}`}>
              <StatusIcon className={`h-3.5 w-3.5 ${!isTerminal ? 'animate-spin' : ''}`} />
              {statusLabel}
            </span>
          </div>
          <button
            onClick={onClose}
            className="inline-flex items-center gap-1 h-7 px-2 rounded-md text-xs text-muted-foreground hover:text-foreground hover:bg-accent"
            title={isTerminal ? 'Close' : 'Hide (install continues in the background)'}
          >
            {isTerminal ? 'Close' : 'Hide'}
            <ChevronDown className="h-3.5 w-3.5" />
          </button>
        </header>

        <div ref={logRef} className="max-h-56 overflow-y-auto px-4 py-3 font-mono text-xs space-y-1 bg-background/40">
          {events.length === 0 && (
            <div className="text-muted-foreground flex items-center gap-2">
              <Loader2 className="h-3 w-3 animate-spin" /> Waiting for the operation to start…
            </div>
          )}
          {events.map((ev) => (
            <div key={ev.id} className="flex items-start gap-2">
              <span className="text-muted-foreground/60 tabular-nums flex-shrink-0">
                {new Date(ev.createdAt).toLocaleTimeString()}
              </span>
              <span className="text-muted-foreground/80 flex-shrink-0 w-20 truncate">[{ev.stage}]</span>
              <span className={levelColor(ev.level)}>{ev.message}</span>
            </div>
          ))}
          {failed && op?.errorMessage && (
            <div className="mt-2 text-status-error whitespace-pre-wrap">{op.errorMessage}</div>
          )}
        </div>
      </div>
    </div>
  );
}
