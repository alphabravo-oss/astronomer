'use client';

import { useEffect, useRef, useState, useCallback } from 'react';
import { useTheme } from 'next-themes';
import { Terminal as TerminalIcon, RefreshCw, X, ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import '@xterm/xterm/css/xterm.css';

interface PodTerminalProps {
  clusterId: string;
  namespace: string;
  pod: string;
  container: string;
  containers?: string[];
  onClose?: () => void;
}

type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

export function PodTerminal({
  clusterId,
  namespace,
  pod,
  container: initialContainer,
  containers = [],
  onClose,
}: PodTerminalProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const termInstanceRef = useRef<import('@xterm/xterm').Terminal | null>(null);
  const fitAddonRef = useRef<import('@xterm/addon-fit').FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const { theme } = useTheme();

  const [selectedContainer, setSelectedContainer] = useState(initialContainer);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');
  const [showContainerDropdown, setShowContainerDropdown] = useState(false);
  const containerDropdownRef = useRef<HTMLDivElement>(null);

  const getThemeColors = useCallback(() => {
    const isDark = theme === 'dark' || theme === 'system';
    return {
      background: isDark ? '#09090b' : '#ffffff',
      foreground: isDark ? '#fafafa' : '#09090b',
      cursor: isDark ? '#fafafa' : '#09090b',
      cursorAccent: isDark ? '#09090b' : '#ffffff',
      selectionBackground: isDark ? 'rgba(255,255,255,0.15)' : 'rgba(0,0,0,0.15)',
      black: isDark ? '#09090b' : '#000000',
      red: '#ef4444',
      green: '#22c55e',
      yellow: '#eab308',
      blue: '#3b82f6',
      magenta: '#a855f7',
      cyan: '#06b6d4',
      white: isDark ? '#fafafa' : '#09090b',
      brightBlack: isDark ? '#71717a' : '#a1a1aa',
      brightRed: '#f87171',
      brightGreen: '#4ade80',
      brightYellow: '#facc15',
      brightBlue: '#60a5fa',
      brightMagenta: '#c084fc',
      brightCyan: '#22d3ee',
      brightWhite: '#ffffff',
    };
  }, [theme]);

  const connectWebSocket = useCallback((term: import('@xterm/xterm').Terminal) => {
    const wsProtocol = typeof window !== 'undefined' && window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsHost = process.env.NEXT_PUBLIC_WS_URL || `${wsProtocol}//${typeof window !== 'undefined' ? window.location.host : 'localhost:3000'}/api/v1/ws`;
    const wsUrl = `${wsHost}/exec/${clusterId}/${namespace}/${pod}/${selectedContainer}/`;
    const token = typeof window !== 'undefined' ? localStorage.getItem('astronomer_token') : null;

    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;
    setStatus('connecting');

    ws.onopen = () => {
      setStatus('connected');
      // Authenticate
      if (token) {
        ws.send(JSON.stringify({ type: 'auth', token }));
      }
      // Send initial terminal size
      ws.send(
        JSON.stringify({
          type: 'resize',
          cols: term.cols,
          rows: term.rows,
        })
      );
    };

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.type === 'output' || data.type === 'stdout') {
          term.write(data.data);
        } else if (data.type === 'error') {
          term.write(`\r\n\x1b[31mError: ${data.message}\x1b[0m\r\n`);
        } else if (typeof data === 'string') {
          term.write(data);
        }
      } catch {
        // Raw text data
        term.write(event.data);
      }
    };

    ws.onerror = () => {
      setStatus('error');
      term.write('\r\n\x1b[31mWebSocket connection error\x1b[0m\r\n');
    };

    ws.onclose = (event) => {
      setStatus('disconnected');
      term.write(`\r\n\x1b[33mConnection closed${event.reason ? `: ${event.reason}` : ''}\x1b[0m\r\n`);
      term.write('\x1b[33mPress the reconnect button to try again\x1b[0m\r\n');
    };

    return ws;
  }, [clusterId, namespace, pod, selectedContainer]);

  // Initialize terminal
  useEffect(() => {
    if (!terminalRef.current) return;

    let term: import('@xterm/xterm').Terminal;
    let fitAddon: import('@xterm/addon-fit').FitAddon;
    let mounted = true;

    const initTerminal = async () => {
      const { Terminal } = await import('@xterm/xterm');
      const { FitAddon } = await import('@xterm/addon-fit');
      const { WebLinksAddon } = await import('@xterm/addon-web-links');

      if (!mounted || !terminalRef.current) return;

      const colors = getThemeColors();

      term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: 'var(--font-mono), "JetBrains Mono", "Fira Code", monospace',
        theme: colors,
        allowProposedApi: true,
        scrollback: 5000,
        convertEol: true,
      });

      fitAddon = new FitAddon();
      term.loadAddon(fitAddon);
      term.loadAddon(new WebLinksAddon());

      term.open(terminalRef.current);
      fitAddon.fit();

      termInstanceRef.current = term;
      fitAddonRef.current = fitAddon;

      term.write(`Connecting to \x1b[36m${pod}\x1b[0m / \x1b[33m${selectedContainer}\x1b[0m ...\r\n`);

      // Handle stdin
      const ws = connectWebSocket(term);

      term.onData((data) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'stdin', data }));
        }
      });

      // Handle resize
      term.onResize(({ cols, rows }) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols, rows }));
        }
      });

      // Handle window resize
      const handleResize = () => {
        if (fitAddonRef.current) {
          fitAddonRef.current.fit();
        }
      };

      window.addEventListener('resize', handleResize);

      // ResizeObserver for container resizing
      const resizeObserver = new ResizeObserver(() => {
        if (fitAddonRef.current) {
          fitAddonRef.current.fit();
        }
      });

      if (terminalRef.current) {
        resizeObserver.observe(terminalRef.current);
      }

      return () => {
        window.removeEventListener('resize', handleResize);
        resizeObserver.disconnect();
      };
    };

    initTerminal();

    return () => {
      mounted = false;
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      if (termInstanceRef.current) {
        termInstanceRef.current.dispose();
        termInstanceRef.current = null;
      }
    };
  }, [selectedContainer, connectWebSocket, getThemeColors, pod]);

  // Update terminal theme when theme changes
  useEffect(() => {
    if (termInstanceRef.current) {
      const colors = getThemeColors();
      termInstanceRef.current.options.theme = colors;
    }
  }, [theme, getThemeColors]);

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
    if (termInstanceRef.current) {
      termInstanceRef.current.clear();
      termInstanceRef.current.write(`Reconnecting to \x1b[36m${pod}\x1b[0m / \x1b[33m${selectedContainer}\x1b[0m ...\r\n`);
      connectWebSocket(termInstanceRef.current);
    }
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
    <div className="flex flex-col h-full rounded-lg border border-border overflow-hidden bg-background">
      {/* Terminal Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-border bg-muted/50">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <TerminalIcon className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-foreground">Terminal</span>
          </div>

          {/* Connection status */}
          <div className="flex items-center gap-1.5">
            <span className={cn('h-2 w-2 rounded-full', statusColors[status])} />
            <span className="text-xs text-muted-foreground">{statusLabels[status]}</span>
          </div>

          {/* Container selector */}
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
          {/* Reconnect button */}
          <button
            onClick={handleReconnect}
            className="inline-flex items-center gap-1 h-6 px-2 rounded text-xs
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Reconnect"
          >
            <RefreshCw className="h-3 w-3" />
            Reconnect
          </button>

          {/* Close button */}
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

      {/* Terminal Body */}
      <div
        ref={terminalRef}
        className="flex-1 min-h-0"
        style={{ padding: '4px' }}
      />
    </div>
  );
}
