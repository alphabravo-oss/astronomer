'use client';

import { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import { usePodLogs, type PodLogsStatus } from '@/lib/hooks';
import { cn } from '@/lib/utils';
import {
  ArrowDown,
  Clock,
  Download,
  Loader2,
  Pause,
  Play,
  Search,
  WrapText,
  X,
} from 'lucide-react';
import type { PodLog } from '@/types';

interface LogsTabProps {
  clusterId: string;
  namespace: string;
  pod: string;
  container?: string;
  // When the WindowManager hides a non-active tab via display:none we still
  // want the WS connection to live, so we mount unconditionally.
  visible: boolean;
  onStatusChange?: (status: PodLogsStatus) => void;
}

// Tab body for a single pod-logs stream living inside the WindowManager.
// All toolbar state (follow/wrap/timestamps/filter) is local to this tab so
// toggling on one tab can't affect another.
export function LogsTab({
  clusterId,
  namespace,
  pod,
  container,
  visible,
  onStatusChange,
}: LogsTabProps) {
  const [follow, setFollow] = useState(true);
  const [wrap, setWrap] = useState(true);
  const [showTimestamps, setShowTimestamps] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const [showSearch, setShowSearch] = useState(false);
  const [tailRange, setTailRange] = useState<TailRange>({ kind: 'lines', n: 500 });
  const scrollRef = useRef<HTMLDivElement>(null);

  const { data: logs, isLoading, status } = usePodLogs(clusterId, namespace, pod, {
    container,
    tailLines: tailRange.kind === 'lines' ? tailRange.n : undefined,
    sinceSeconds: tailRange.kind === 'seconds' ? tailRange.s : undefined,
    noTail: tailRange.kind === 'all',
    follow,
  });

  useEffect(() => {
    onStatusChange?.(status);
  }, [status, onStatusChange]);

  // Auto-scroll while following; mirror logic from the standalone viewer
  // so behavior is identical. The isAutoScrolling guard prevents our own
  // programmatic scroll from being misinterpreted as a user pause.
  const isAutoScrolling = useRef(false);
  useEffect(() => {
    if (follow && scrollRef.current) {
      isAutoScrolling.current = true;
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
      requestAnimationFrame(() => {
        isAutoScrolling.current = false;
      });
    }
  }, [logs, follow]);

  // Scroll listener: pause follow when the user scrolls away from the
  // bottom. We deliberately do NOT auto-resume follow when the user
  // scrolls back to the bottom — the previous "smart resume" logic
  // fought the explicit Pause button, since pausing collapses the
  // log list which scrolls to bottom which re-armed follow. Resume is
  // now an explicit click (the Following button or the "Scroll to
  // bottom and follow" bar).
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    function onScroll() {
      if (isAutoScrolling.current || !el) return;
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 16;
      if (!atBottom) {
        setFollow((prev) => (prev ? false : prev));
      }
    }
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, []);

  const filteredLogs = useMemo(() => {
    if (!searchQuery.trim() || !logs) return logs || [];
    const q = searchQuery.toLowerCase();
    return logs.filter((log) => (log.message || '').toLowerCase().includes(q));
  }, [logs, searchQuery]);

  const handleDownload = useCallback(() => {
    if (!filteredLogs.length) return;
    const content = filteredLogs
      .map((log) => `${showTimestamps ? log.timestamp + ' ' : ''}${log.message}`)
      .join('\n');
    const blob = new Blob([content], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${pod}-${container || 'main'}-logs.txt`;
    a.click();
    URL.revokeObjectURL(url);
  }, [filteredLogs, pod, container, showTimestamps]);

  const getLogLineClass = (log: PodLog) => {
    const msg = (log.message || '').toLowerCase();
    if (log.level === 'error' || msg.includes('error') || msg.includes('fatal')) {
      return 'log-error';
    }
    if (log.level === 'warn' || msg.includes('warn')) {
      return 'log-warn';
    }
    return '';
  };

  return (
    <div
      className="flex flex-col h-full bg-background"
      // Hide the inactive tab without unmounting so the WS connection
      // stays alive and the log buffer doesn't reset on tab switch.
      style={{ display: visible ? 'flex' : 'none' }}
    >
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2 px-3 py-1.5 bg-muted/40 border-b border-border flex-wrap">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono truncate max-w-[280px]" title={`${namespace}/${pod}`}>
            {namespace}/{pod}
          </span>
          {container && (
            <span className="font-mono text-foreground/80">· {container}</span>
          )}
          <TailRangeSelect value={tailRange} onChange={setTailRange} />
        </div>

        <div className="flex items-center gap-1">
          <button
            onClick={() => setShowTimestamps((v) => !v)}
            className={cn(
              'inline-flex items-center h-6 px-1.5 rounded text-2xs transition-colors',
              showTimestamps
                ? 'bg-accent text-foreground'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title="Toggle timestamps"
          >
            <Clock className="h-3 w-3" />
          </button>
          <button
            onClick={() => setWrap((v) => !v)}
            className={cn(
              'inline-flex items-center h-6 px-1.5 rounded text-2xs transition-colors',
              wrap
                ? 'bg-accent text-foreground'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title="Toggle line wrap"
          >
            <WrapText className="h-3 w-3" />
          </button>
          <button
            onClick={() => setShowSearch((v) => !v)}
            className={cn(
              'inline-flex items-center h-6 px-1.5 rounded text-2xs transition-colors',
              showSearch
                ? 'bg-accent text-foreground'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title="Filter logs"
          >
            <Search className="h-3 w-3" />
          </button>
          <button
            onClick={() => setFollow((v) => !v)}
            className={cn(
              'inline-flex items-center gap-1 h-6 px-1.5 rounded text-2xs transition-colors',
              follow
                ? 'bg-status-success/10 text-status-success'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title={follow ? 'Stop following' : 'Follow logs'}
          >
            {follow ? <Pause className="h-3 w-3" /> : <Play className="h-3 w-3" />}
            <span className="hidden sm:inline">{follow ? 'Following' : 'Follow'}</span>
          </button>
          <button
            onClick={handleDownload}
            className="inline-flex items-center h-6 px-1.5 rounded text-2xs
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Download logs"
          >
            <Download className="h-3 w-3" />
          </button>
        </div>
      </div>

      {showSearch && (
        <div className="flex items-center gap-2 px-3 py-1 bg-muted/30 border-b border-border">
          <Search className="h-3 w-3 text-muted-foreground flex-shrink-0" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Filter logs..."
            className="flex-1 h-5 bg-transparent text-xs text-foreground placeholder:text-muted-foreground
              focus:outline-none"
            autoFocus
          />
          {searchQuery && (
            <span className="text-2xs text-muted-foreground">
              {filteredLogs.length} matches
            </span>
          )}
          <button
            onClick={() => {
              setShowSearch(false);
              setSearchQuery('');
            }}
            className="text-muted-foreground hover:text-foreground"
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      )}

      {/* Log content */}
      <div
        ref={scrollRef}
        className={cn(
          'log-viewer flex-1 min-h-0 overflow-y-auto p-3',
          wrap ? 'overflow-x-hidden' : 'overflow-x-auto'
        )}
      >
        {isLoading ? (
          <div className="flex items-center justify-center h-full text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin mr-2" />
            <span className="text-xs">Loading logs...</span>
          </div>
        ) : filteredLogs.length === 0 ? (
          <div className="flex items-center justify-center h-full text-zinc-600 text-xs">
            {searchQuery ? 'No matching log lines' : 'No logs available'}
          </div>
        ) : (
          filteredLogs.map((log, i) => (
            <div
              key={i}
              className={cn(
                'flex gap-2 hover:bg-white/[0.02] px-1 -mx-1 rounded',
                getLogLineClass(log)
              )}
            >
              {showTimestamps && (
                <span className="log-timestamp flex-shrink-0 whitespace-nowrap">
                  {new Date(log.timestamp).toLocaleTimeString()}
                </span>
              )}
              <span
                className={cn(
                  wrap ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'
                )}
              >
                {log.message}
              </span>
            </div>
          ))
        )}
      </div>

      {!follow && filteredLogs.length > 0 && (
        <button
          onClick={() => {
            setFollow(true);
            if (scrollRef.current) {
              scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
            }
          }}
          className="flex items-center justify-center gap-1.5 py-1
            bg-muted/80 backdrop-blur-sm border-t border-border text-2xs text-muted-foreground
            hover:text-foreground transition-colors"
        >
          <ArrowDown className="h-3 w-3" />
          Scroll to bottom and follow
        </button>
      )}
    </div>
  );
}

// TailRange — Rancher-style range selector: either a line count, a time
// window in seconds, or unlimited ("All"). The shape is tagged so the call
// site can fan out to the right query param (`tail_lines` vs `since_seconds`
// vs neither) without ambiguity.
export type TailRange =
  | { kind: 'lines'; n: number }
  | { kind: 'seconds'; s: number }
  | { kind: 'all' };

type TailRangeOption =
  | { kind: 'lines'; n: number; label: string }
  | { kind: 'seconds'; s: number; label: string }
  | { kind: 'all'; label: string };

const TAIL_LINE_OPTIONS: TailRangeOption[] = [
  { kind: 'lines', n: 100, label: '100 lines' },
  { kind: 'lines', n: 500, label: '500 lines' },
  { kind: 'lines', n: 1000, label: '1000 lines' },
  { kind: 'lines', n: 5000, label: '5000 lines' },
];

const TAIL_TIME_OPTIONS: TailRangeOption[] = [
  { kind: 'seconds', s: 5 * 60, label: 'Last 5 minutes' },
  { kind: 'seconds', s: 15 * 60, label: 'Last 15 minutes' },
  { kind: 'seconds', s: 60 * 60, label: 'Last 1 hour' },
  { kind: 'seconds', s: 12 * 60 * 60, label: 'Last 12 hours' },
  { kind: 'seconds', s: 24 * 60 * 60, label: 'Last 24 hours' },
  { kind: 'all', label: 'All' },
];

function labelForRange(r: TailRange): string {
  if (r.kind === 'lines') {
    const match = TAIL_LINE_OPTIONS.find((o) => o.kind === 'lines' && o.n === r.n);
    return match?.label ?? `${r.n} lines`;
  }
  if (r.kind === 'seconds') {
    const match = TAIL_TIME_OPTIONS.find((o) => o.kind === 'seconds' && o.s === r.s);
    return match?.label ?? `Last ${r.s}s`;
  }
  return 'All';
}

function rangeEquals(a: TailRange, b: TailRangeOption): boolean {
  if (a.kind === 'lines' && b.kind === 'lines') return a.n === b.n;
  if (a.kind === 'seconds' && b.kind === 'seconds') return a.s === b.s;
  if (a.kind === 'all' && b.kind === 'all') return true;
  return false;
}

// TailRangeSelect — a styled dropdown for the Rancher-style tail-range knob.
// Native <select> elements were rendering with browser-default chrome that
// clashed with the dark/muted toolbar style; this dropdown matches the rest
// of the toolbar (h-6, 2xs text, border, hover state). The popover lists
// line-count options first, then a thin separator, then time-window options
// and "All".
function TailRangeSelect({
  value,
  onChange,
}: {
  value: TailRange;
  onChange: (v: TailRange) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDocClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, [open]);

  const pick = (opt: TailRangeOption) => {
    if (opt.kind === 'lines') onChange({ kind: 'lines', n: opt.n });
    else if (opt.kind === 'seconds') onChange({ kind: 'seconds', s: opt.s });
    else onChange({ kind: 'all' });
    setOpen(false);
  };

  const renderOption = (opt: TailRangeOption) => {
    const selected = rangeEquals(value, opt);
    return (
      <button
        key={opt.label}
        onClick={() => pick(opt)}
        className={cn(
          'w-full flex items-center px-2 py-1 rounded text-2xs transition-colors',
          selected
            ? 'bg-accent text-foreground'
            : 'text-muted-foreground hover:bg-accent hover:text-foreground'
        )}
      >
        <span className="tabular-nums">{opt.label}</span>
      </button>
    );
  };

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 h-6 px-2 rounded border border-border bg-background
          text-2xs text-foreground hover:bg-accent transition-colors
          focus:outline-none focus:ring-1 focus:ring-ring"
        title="Tail range"
      >
        <span className="tabular-nums">{labelForRange(value)}</span>
        <svg
          className={cn(
            'h-3 w-3 text-muted-foreground transition-transform',
            open && 'rotate-180'
          )}
          viewBox="0 0 12 12"
          fill="none"
        >
          <path d="M3 5l3 3 3-3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open && (
        <div className="absolute left-0 top-full mt-1 w-36 rounded-md border border-border bg-popover p-1 shadow-lg z-50">
          {TAIL_LINE_OPTIONS.map(renderOption)}
          <div className="my-1 border-t border-border" />
          {TAIL_TIME_OPTIONS.map(renderOption)}
        </div>
      )}
    </div>
  );
}
