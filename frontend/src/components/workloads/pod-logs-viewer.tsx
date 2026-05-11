'use client';

import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { usePodLogs } from '@/lib/hooks';
import type { Pod, PodLog } from '@/types';
import { cn } from '@/lib/utils';
import {
  Download,
  Search,
  ArrowDown,
  Pause,
  Play,
  X,
  Clock,
  Loader2,
} from 'lucide-react';

interface PodLogsViewerProps {
  clusterId: string;
  namespace: string;
  pods: Pod[];
  selectedPod: string;
  onPodChange: (pod: string) => void;
  className?: string;
}

export function PodLogsViewer({
  clusterId,
  namespace,
  pods,
  selectedPod,
  onPodChange,
  className,
}: PodLogsViewerProps) {
  const [follow, setFollow] = useState(true);
  const [showTimestamps, setShowTimestamps] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const [showSearch, setShowSearch] = useState(false);
  const [tailLines, setTailLines] = useState(500);
  const scrollRef = useRef<HTMLDivElement>(null);

  const activePod = pods.find((p) => p.name === selectedPod) || pods[0];
  const podName = activePod?.name || '';
  const containers = activePod?.containers || [];
  const [selectedContainer, setSelectedContainer] = useState(containers[0]?.name || '');

  // Update container when pod changes
  useEffect(() => {
    if (containers.length > 0 && !containers.find((c) => c.name === selectedContainer)) {
      setSelectedContainer(containers[0].name);
    }
  }, [containers, selectedContainer]);

  const { data: logs, isLoading, stopStreaming } = usePodLogs(
    clusterId,
    namespace,
    podName,
    {
      container: selectedContainer,
      tailLines,
      follow,
    }
  );

  // Auto-scroll when following. We pin to the bottom whenever `follow` is
  // true and new lines arrive. The `isAutoScrolling` ref tells the scroll
  // handler below to ignore the programmatic scrollTop write it's about to
  // see — otherwise the handler would interpret our own scroll as a user
  // scroll-up and immediately flip `follow` off, defeating the feature.
  const isAutoScrolling = useRef(false);
  useEffect(() => {
    if (follow && scrollRef.current) {
      isAutoScrolling.current = true;
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
      // Release the flag after the browser has painted the scroll write.
      requestAnimationFrame(() => {
        isAutoScrolling.current = false;
      });
    }
  }, [logs, follow]);

  // Detect user scroll-up to pause follow. A threshold of 16px lets us treat
  // "near the bottom" as still-following so that fractional pixel rounding
  // from the browser doesn't constantly trip the pause. Scrolling back to
  // within the threshold re-enables follow automatically.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    function onScroll() {
      if (isAutoScrolling.current || !el) return;
      const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 16;
      setFollow((prev) => {
        if (prev && !atBottom) return false;
        if (!prev && atBottom) return true;
        return prev;
      });
    }
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, []);

  // Filter logs by search
  const filteredLogs = useMemo(() => {
    if (!searchQuery.trim() || !logs) return logs || [];
    const q = searchQuery.toLowerCase();
    return logs.filter((log) => (log.message || '').toLowerCase().includes(q));
  }, [logs, searchQuery]);

  // Keyboard shortcut for search
  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'f') {
        e.preventDefault();
        setShowSearch(true);
      }
      if (e.key === 'Escape') {
        setShowSearch(false);
        setSearchQuery('');
      }
    }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, []);

  const handleDownload = useCallback(() => {
    if (!filteredLogs.length) return;
    const content = filteredLogs
      .map((log) => `${showTimestamps ? log.timestamp + ' ' : ''}${log.message}`)
      .join('\n');
    const blob = new Blob([content], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${podName}-${selectedContainer}-logs.txt`;
    a.click();
    URL.revokeObjectURL(url);
  }, [filteredLogs, podName, selectedContainer, showTimestamps]);

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

  if (!activePod) {
    return (
      <div className="flex items-center justify-center h-64 text-muted-foreground text-xs">
        No pods available
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2 px-3 py-2 bg-muted/50 border-b border-border flex-wrap">
        <div className="flex items-center gap-2">
          {/* Pod selector */}
          <select
            value={podName}
            onChange={(e) => onPodChange(e.target.value)}
            className="h-7 px-2 rounded border border-border bg-background text-xs
              focus:outline-none focus:ring-1 focus:ring-ring max-w-[200px]"
          >
            {pods.map((pod) => (
              <option key={pod.name} value={pod.name}>
                {pod.name}
              </option>
            ))}
          </select>

          {/* Container selector */}
          {containers.length > 1 && (
            <select
              value={selectedContainer}
              onChange={(e) => setSelectedContainer(e.target.value)}
              className="h-7 px-2 rounded border border-border bg-background text-xs
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              {containers.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                </option>
              ))}
            </select>
          )}

          {/* Tail lines */}
          <select
            value={tailLines}
            onChange={(e) => setTailLines(Number(e.target.value))}
            className="h-7 px-2 rounded border border-border bg-background text-xs
              focus:outline-none focus:ring-1 focus:ring-ring"
          >
            <option value={100}>100 lines</option>
            <option value={500}>500 lines</option>
            <option value={1000}>1000 lines</option>
            <option value={5000}>5000 lines</option>
          </select>
        </div>

        <div className="flex items-center gap-1">
          {/* Timestamps toggle */}
          <button
            onClick={() => setShowTimestamps(!showTimestamps)}
            className={cn(
              'inline-flex items-center gap-1 h-7 px-2 rounded text-xs transition-colors',
              showTimestamps
                ? 'bg-accent text-foreground'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title="Toggle timestamps"
          >
            <Clock className="h-3 w-3" />
          </button>

          {/* Search toggle */}
          <button
            onClick={() => setShowSearch(!showSearch)}
            className={cn(
              'inline-flex items-center gap-1 h-7 px-2 rounded text-xs transition-colors',
              showSearch
                ? 'bg-accent text-foreground'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title="Search logs"
          >
            <Search className="h-3 w-3" />
          </button>

          {/* Follow toggle */}
          <button
            onClick={() => setFollow(!follow)}
            className={cn(
              'inline-flex items-center gap-1 h-7 px-2 rounded text-xs transition-colors',
              follow
                ? 'bg-status-success/10 text-status-success'
                : 'text-muted-foreground hover:text-foreground hover:bg-accent'
            )}
            title={follow ? 'Stop following' : 'Follow logs'}
          >
            {follow ? <Pause className="h-3 w-3" /> : <Play className="h-3 w-3" />}
            <span className="hidden sm:inline">{follow ? 'Following' : 'Follow'}</span>
          </button>

          {/* Download */}
          <button
            onClick={handleDownload}
            className="inline-flex items-center gap-1 h-7 px-2 rounded text-xs
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Download logs"
          >
            <Download className="h-3 w-3" />
          </button>
        </div>
      </div>

      {/* Search bar */}
      {showSearch && (
        <div className="flex items-center gap-2 px-3 py-1.5 bg-muted/30 border-b border-border">
          <Search className="h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Filter logs..."
            className="flex-1 h-6 bg-transparent text-xs text-foreground placeholder:text-muted-foreground
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
        className={cn("log-viewer overflow-y-auto overflow-x-hidden p-3", className || "h-[500px]")}
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
            <div key={i} className={cn('flex gap-2 hover:bg-white/[0.02] px-1 -mx-1 rounded', getLogLineClass(log))}>
              {showTimestamps && (
                <span className="log-timestamp flex-shrink-0 whitespace-nowrap">
                  {new Date(log.timestamp).toLocaleTimeString()}
                </span>
              )}
              <span className="break-all whitespace-pre-wrap">{log.message}</span>
            </div>
          ))
        )}
      </div>

      {/* Auto-scroll indicator */}
      {!follow && (
        <button
          onClick={() => {
            setFollow(true);
            if (scrollRef.current) {
              scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
            }
          }}
          className="sticky bottom-0 w-full flex items-center justify-center gap-1.5 py-1.5
            bg-muted/80 backdrop-blur-sm border-t border-border text-xs text-muted-foreground
            hover:text-foreground transition-colors"
        >
          <ArrowDown className="h-3 w-3" />
          Scroll to bottom and follow
        </button>
      )}
    </div>
  );
}
