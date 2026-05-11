'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useWindowManagerStore, type WindowTab } from '@/lib/window-manager-store';
import { cn } from '@/lib/utils';
import {
  ChevronUp,
  FileText,
  Maximize2,
  Minimize2,
  Terminal as TerminalIcon,
  X,
} from 'lucide-react';
import { LogsTab } from './logs-tab';
import { ExecTab } from './exec-tab';

// Per-tab connection state, mirrored from each tab body via the
// `onStatusChange` callback. Kept here in component-local state so the
// chips can render a live pill without coupling to the global store.
type ChipStatus = 'streaming' | 'connecting' | 'disconnected' | 'idle';

function normalizeStatus(s: string | undefined): ChipStatus {
  if (s === 'streaming' || s === 'connected') return 'streaming';
  if (s === 'connecting') return 'connecting';
  if (s === 'disconnected' || s === 'error') return 'disconnected';
  return 'idle';
}

export function WindowManager() {
  const tabs = useWindowManagerStore((s) => s.tabs);
  const activeTabId = useWindowManagerStore((s) => s.activeTabId);
  const open = useWindowManagerStore((s) => s.open);
  const minimized = useWindowManagerStore((s) => s.minimized);
  const height = useWindowManagerStore((s) => s.height);
  const setActive = useWindowManagerStore((s) => s.setActive);
  const closeTab = useWindowManagerStore((s) => s.closeTab);
  const closeAll = useWindowManagerStore((s) => s.closeAll);
  const toggleMinimize = useWindowManagerStore((s) => s.toggleMinimize);
  const setHeight = useWindowManagerStore((s) => s.setHeight);

  const [tabStatuses, setTabStatuses] = useState<Record<string, ChipStatus>>({});
  const [maximized, setMaximized] = useState(false);
  // Track height before maximize so we can restore the user's preferred
  // size when they un-maximize.
  const preMaxHeightRef = useRef<number | null>(null);

  const handleStatusChange = useCallback((id: string, status: string) => {
    setTabStatuses((prev) => {
      const next = normalizeStatus(status);
      if (prev[id] === next) return prev;
      return { ...prev, [id]: next };
    });
  }, []);

  // Drag-to-resize the top edge. We track delta against the height at
  // drag-start rather than absolute mouse position to avoid jitter when
  // the cursor temporarily leaves the handle.
  const dragRef = useRef<{ startY: number; startHeight: number } | null>(null);
  const onDragStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      dragRef.current = { startY: e.clientY, startHeight: height };
      document.body.style.cursor = 'row-resize';
      document.body.style.userSelect = 'none';
    },
    [height]
  );

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!dragRef.current) return;
      const delta = dragRef.current.startY - e.clientY;
      setHeight(dragRef.current.startHeight + delta);
    }
    function onUp() {
      if (!dragRef.current) return;
      dragRef.current = null;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    }
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
    return () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
  }, [setHeight]);

  const handleMaximizeToggle = useCallback(() => {
    if (maximized) {
      if (preMaxHeightRef.current != null) {
        setHeight(preMaxHeightRef.current);
      }
      setMaximized(false);
    } else {
      preMaxHeightRef.current = height;
      const target = typeof window !== 'undefined' ? window.innerHeight - 80 : height;
      setHeight(target);
      setMaximized(true);
    }
  }, [maximized, height, setHeight]);

  // Reset maximize tracking if user manually drag-resizes away from the
  // maximized state.
  useEffect(() => {
    if (maximized && typeof window !== 'undefined') {
      const target = window.innerHeight - 80;
      if (Math.abs(height - target) > 4) {
        setMaximized(false);
      }
    }
  }, [height, maximized]);

  if (!open || tabs.length === 0) return null;

  // Minimized — render only a thin strip at the bottom of the viewport.
  if (minimized) {
    return (
      <div
        className="fixed left-0 right-0 bottom-0 z-40 flex items-center gap-1 px-2 py-1
          border-t border-border bg-card/95 backdrop-blur-sm"
      >
        <button
          onClick={() => toggleMinimize()}
          className="inline-flex items-center gap-1 h-6 px-2 rounded text-2xs
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          title="Restore"
        >
          <ChevronUp className="h-3 w-3" />
          <span>Console</span>
        </button>
        <div className="flex items-center gap-1 overflow-x-auto">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => {
                setActive(t.id);
              }}
              className={cn(
                'inline-flex items-center gap-1.5 h-6 px-2 rounded text-2xs whitespace-nowrap transition-colors',
                t.id === activeTabId
                  ? 'bg-accent text-foreground'
                  : 'text-muted-foreground hover:text-foreground hover:bg-accent/60'
              )}
            >
              <StatusDot status={tabStatuses[t.id] ?? 'idle'} />
              <TabIcon kind={t.kind} />
              <span className="font-mono truncate max-w-[160px]" title={`${t.pod}/${t.container ?? ''}`}>
                {shortLabel(t)}
              </span>
            </button>
          ))}
        </div>
        <div className="ml-auto" />
        <button
          onClick={() => closeAll()}
          className="inline-flex items-center justify-center h-6 w-6 rounded
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          title="Close all"
        >
          <X className="h-3 w-3" />
        </button>
      </div>
    );
  }

  return (
    <div
      className="fixed left-0 right-0 bottom-0 z-40 flex flex-col border-t border-border
        bg-card shadow-xl"
      style={{ height: `${height}px` }}
    >
      {/* Resize handle */}
      <div
        onMouseDown={onDragStart}
        className="h-1 -mt-px cursor-row-resize hover:bg-primary/40 transition-colors shrink-0"
        style={{ marginBottom: '-1px' }}
      />

      {/* Tab strip */}
      <div className="flex items-stretch border-b border-border bg-muted/30 shrink-0">
        <div className="flex items-stretch overflow-x-auto flex-1">
          {tabs.map((t) => {
            const isActive = t.id === activeTabId;
            return (
              <button
                key={t.id}
                onClick={() => setActive(t.id)}
                className={cn(
                  'group inline-flex items-center gap-1.5 h-8 px-3 text-2xs whitespace-nowrap',
                  'border-r border-border transition-colors',
                  isActive
                    ? 'bg-background text-foreground'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent/40'
                )}
                title={`${t.namespace}/${t.pod}${t.container ? '/' + t.container : ''}`}
              >
                <StatusDot status={tabStatuses[t.id] ?? 'idle'} />
                <TabIcon kind={t.kind} />
                <span className="font-mono">
                  {t.pod}
                  {t.container ? <span className="text-muted-foreground"> · {t.container}</span> : null}
                </span>
                <span
                  role="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    closeTab(t.id);
                  }}
                  className="ml-1 inline-flex items-center justify-center h-4 w-4 rounded
                    text-muted-foreground/70 hover:text-foreground hover:bg-accent/80"
                >
                  <X className="h-3 w-3" />
                </span>
              </button>
            );
          })}
        </div>

        {/* Right-end controls */}
        <div className="flex items-center gap-0.5 px-2 border-l border-border">
          <button
            onClick={handleMaximizeToggle}
            className="inline-flex items-center justify-center h-6 w-6 rounded
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title={maximized ? 'Restore size' : 'Maximize'}
          >
            {maximized ? <Minimize2 className="h-3 w-3" /> : <Maximize2 className="h-3 w-3" />}
          </button>
          <button
            onClick={() => toggleMinimize()}
            className="inline-flex items-center justify-center h-6 w-6 rounded
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Minimize"
          >
            <ChevronUp className="h-3 w-3 rotate-180" />
          </button>
          <button
            onClick={() => closeAll()}
            className="inline-flex items-center justify-center h-6 w-6 rounded
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Close all"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      {/* Bodies — all mounted, only the active one visible. */}
      <div className="flex-1 min-h-0 relative">
        {tabs.map((t) => (
          <div
            key={t.id}
            className="absolute inset-0"
            // Hide rather than unmount: each tab owns a live WebSocket /
            // xterm buffer that must survive tab switches.
            style={{ display: t.id === activeTabId ? 'block' : 'none' }}
          >
            {t.kind === 'logs' ? (
              <LogsTab
                clusterId={t.clusterId}
                namespace={t.namespace}
                pod={t.pod}
                container={t.container}
                visible={t.id === activeTabId}
                onStatusChange={(s) => handleStatusChange(t.id, s)}
              />
            ) : (
              <ExecTab
                clusterId={t.clusterId}
                namespace={t.namespace}
                pod={t.pod}
                container={t.container}
                visible={t.id === activeTabId}
                onStatusChange={(s) => handleStatusChange(t.id, s)}
              />
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function TabIcon({ kind }: { kind: WindowTab['kind'] }) {
  return kind === 'logs' ? (
    <FileText className="h-3 w-3" />
  ) : (
    <TerminalIcon className="h-3 w-3" />
  );
}

function StatusDot({ status }: { status: ChipStatus }) {
  const cls =
    status === 'streaming'
      ? 'bg-status-success animate-pulse'
      : status === 'connecting'
        ? 'bg-status-warning'
        : status === 'disconnected'
          ? 'bg-status-error'
          : 'bg-muted-foreground/40';
  return <span className={cn('h-1.5 w-1.5 rounded-full', cls)} />;
}

function shortLabel(t: WindowTab): string {
  const podShort = t.pod.length > 18 ? t.pod.slice(0, 15) + '...' : t.pod;
  return t.container ? `${podShort}·${t.container}` : podShort;
}
