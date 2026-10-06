import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  useWindowManagerStore,
  type WindowTab,
} from "@/lib/window-manager-store";
import { cn } from "@/lib/utils";
import { StatusDot } from "@/components/ui/status-badge";
import {
  ChevronUp,
  FileText,
  Maximize2,
  Minimize2,
  Terminal as TerminalIcon,
  X,
} from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import { BARE_BUTTON } from "@/lib/bare-button";
// Console transports and the terminal runtime are loaded only when a tab opens.
const LogsTab = lazy(() =>
  import("./logs-tab").then((module) => ({ default: module.LogsTab })),
);
const ExecTab = lazy(() =>
  import("./exec-tab").then((module) => ({ default: module.ExecTab })),
);
const ClusterShell = lazy(() =>
  import("@/components/clusters/cluster-shell").then((module) => ({
    default: module.ClusterShell,
  })),
);

// Per-tab connection state, mirrored from each tab body via the
// `onStatusChange` callback. Kept here in component-local state so the
// chips can render a live pill without coupling to the global store.
type ChipStatus = "streaming" | "connecting" | "disconnected" | "idle";

function normalizeStatus(s: string | undefined): ChipStatus {
  if (s === "streaming" || s === "connected") return "streaming";
  if (s === "opening" || s === "connecting") return "connecting";
  if (s === "disconnected" || s === "error") return "disconnected";
  return "idle";
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

  const [tabStatuses, setTabStatuses] = useState<Record<string, ChipStatus>>(
    {},
  );
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
      document.body.style.cursor = "row-resize";
      document.body.style.userSelect = "none";
    },
    [height],
  );

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!dragRef.current) return;
      const delta = dragRef.current.startY - e.clientY;
      if (Math.abs(delta) > 4) setMaximized(false);
      setHeight(dragRef.current.startHeight + delta);
    }
    function onUp() {
      if (!dragRef.current) return;
      dragRef.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
    return () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
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
      const target =
        typeof window !== "undefined" ? window.innerHeight - 80 : height;
      setHeight(target);
      setMaximized(true);
    }
  }, [maximized, height, setHeight]);

  if (!open || tabs.length === 0) return null;

  // Minimized — render only a thin strip at the bottom of the viewport.
  if (minimized) {
    return (
      <div
        className="fixed left-0 right-0 bottom-0 z-40 flex items-center gap-1 px-2 py-1
          border-t border-border bg-card/95 backdrop-blur-xs"
      >
        <ActionButton
          {...BARE_BUTTON}
          tooltip="Restore"
          onClick={() => toggleMinimize()}
          className="inline-flex items-center gap-1 h-6 px-2 rounded-sm text-2xs
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors font-normal"
        >
          <ChevronUp className="h-3 w-3" />
          <span>Console</span>
        </ActionButton>
        <div className="flex items-center gap-1 overflow-x-auto">
          {tabs.map((t) => (
            <ActionButton
              {...BARE_BUTTON}
              key={t.id}
              onClick={() => {
                setActive(t.id);
              }}
              className={cn(
                "inline-flex items-center gap-1.5 h-6 px-2 rounded-sm text-2xs whitespace-nowrap transition-colors font-normal",
                t.id === activeTabId
                  ? "bg-accent text-foreground"
                  : "text-muted-foreground hover:text-foreground hover:bg-accent/60",
              )}
            >
              <TabStatusDot status={tabStatuses[t.id] ?? "idle"} />
              <TabIcon kind={t.kind} />
              <Tooltip content={tabDescription(t)}>
                <span className="font-mono truncate max-w-40">
                  {shortLabel(t)}
                </span>
              </Tooltip>
            </ActionButton>
          ))}
        </div>
        <div className="ml-auto" />
        <ConsoleIconButton label="Close all" onClick={closeAll}>
          <X className="h-3 w-3" />
        </ConsoleIconButton>
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
      <ActionButton
        {...BARE_BUTTON}
        aria-label={`Resize console, currently ${height} pixels. Use up and down arrow keys.`}
        onMouseDown={onDragStart}
        onKeyDown={(event) => {
          if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
          event.preventDefault();
          setHeight(height + (event.key === "ArrowUp" ? 24 : -24));
        }}
        className="h-1 w-full border-0 p-0 -mt-px cursor-row-resize hover:bg-primary/40 transition-colors shrink-0 focus:bg-primary/40 focus:outline-hidden inline-block font-normal whitespace-normal"
        style={{ marginBottom: "-1px" }}
      />

      {/* Tab strip */}
      <div className="flex items-stretch border-b border-border bg-muted/30 shrink-0">
        <div className="flex items-stretch overflow-x-auto flex-1">
          {tabs.map((t) => {
            const isActive = t.id === activeTabId;
            return (
              <Tooltip key={t.id} content={tabDescription(t)}>
                <div
                  className={cn(
                    "group inline-flex items-center gap-1.5 h-8 px-3 text-2xs whitespace-nowrap",
                    "border-r border-border transition-colors",
                    isActive
                      ? "bg-background text-foreground"
                      : "text-muted-foreground hover:text-foreground hover:bg-accent/40",
                  )}
                >
                  <ActionButton
                    {...BARE_BUTTON}
                    onClick={() => setActive(t.id)}
                    aria-pressed={isActive}
                    className="inline-flex min-w-0 items-center gap-1.5 focus:outline-hidden focus:ring-2 focus:ring-ring font-normal whitespace-normal shrink"
                  >
                    <TabStatusDot status={tabStatuses[t.id] ?? "idle"} />
                    <TabIcon kind={t.kind} />
                    <span className="font-mono">{tabLabel(t)}</span>
                  </ActionButton>
                  <ActionButton
                    {...BARE_BUTTON}
                    onClick={() => closeTab(t.id)}
                    aria-label={`Close ${tabLabel(t)} tab`}
                    className="ml-1 inline-flex items-center justify-center h-4 w-4 rounded-sm
                    text-muted-foreground/70 hover:text-foreground hover:bg-accent/80 font-normal"
                  >
                    <X className="h-3 w-3" />
                  </ActionButton>
                </div>
              </Tooltip>
            );
          })}
        </div>

        {/* Right-end controls */}
        <div className="flex items-center gap-0.5 px-2 border-l border-border">
          <ConsoleIconButton
            label={maximized ? "Restore size" : "Maximize"}
            onClick={handleMaximizeToggle}
          >
            {maximized ? (
              <Minimize2 className="h-3 w-3" />
            ) : (
              <Maximize2 className="h-3 w-3" />
            )}
          </ConsoleIconButton>
          <ConsoleIconButton label="Minimize" onClick={toggleMinimize}>
            <ChevronUp className="h-3 w-3 rotate-180" />
          </ConsoleIconButton>
          <ConsoleIconButton label="Close all" onClick={closeAll}>
            <X className="h-3.5 w-3.5" />
          </ConsoleIconButton>
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
            style={{ display: t.id === activeTabId ? "block" : "none" }}
          >
            <Suspense
              fallback={
                <p role="status" className="p-4 text-sm text-muted-foreground">
                  Loading console…
                </p>
              }
            >
              {t.kind === "logs" ? (
                <LogsTab
                  clusterId={t.clusterId}
                  namespace={t.namespace}
                  pod={t.pod}
                  container={t.container}
                  visible={t.id === activeTabId}
                  onStatusChange={(s) => handleStatusChange(t.id, s)}
                />
              ) : t.kind === "exec" ? (
                <ExecTab
                  clusterId={t.clusterId}
                  namespace={t.namespace}
                  pod={t.pod}
                  container={t.container}
                  visible={t.id === activeTabId}
                  onStatusChange={(s) => handleStatusChange(t.id, s)}
                />
              ) : (
                <ClusterShell
                  clusterId={t.clusterId}
                  visible={t.id === activeTabId}
                  onStatusChange={(s) => handleStatusChange(t.id, s)}
                />
              )}
            </Suspense>
          </div>
        ))}
      </div>
    </div>
  );
}

function ConsoleIconButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <ActionButton
      {...BARE_BUTTON}
      tooltip={label}
      aria-label={label}
      onClick={() => onClick()}
      className="inline-flex h-6 w-6 items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground font-normal"
    >
      {children}
    </ActionButton>
  );
}

function TabIcon({ kind }: { kind: WindowTab["kind"] }) {
  return kind === "logs" ? (
    <FileText className="h-3 w-3" />
  ) : (
    <TerminalIcon className="h-3 w-3" />
  );
}

/**
 * Decorative connection-state dot for a tab strip entry. `aria-hidden`
 * because the enclosing button already carries the tab's accessible name;
 * the dot only adds a supplementary visual cue.
 */
function TabStatusDot({ status }: { status: ChipStatus }) {
  return (
    <span aria-hidden="true">
      <StatusDot status={status} pulse={status === "streaming"} />
    </span>
  );
}

function shortLabel(t: WindowTab): string {
  if (t.kind === "shell") return t.clusterName || t.clusterId;
  const podShort = t.pod.length > 18 ? t.pod.slice(0, 15) + "..." : t.pod;
  return t.container ? `${podShort}·${t.container}` : podShort;
}

function tabLabel(t: WindowTab): string {
  if (t.kind === "shell") {
    return `Shell · ${t.clusterName || t.clusterId}`;
  }
  return t.container ? `${t.pod} · ${t.container}` : t.pod;
}

function tabDescription(t: WindowTab): string {
  if (t.kind === "shell") {
    return `Audited kubectl shell for ${t.clusterName || t.clusterId}`;
  }
  return `${t.namespace}/${t.pod}${t.container ? `/${t.container}` : ""}`;
}
