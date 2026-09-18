import { useCallback, useEffect } from "react";
import { TerminalSquare } from "lucide-react";

import { openClusterShellWindow } from "@/lib/window-manager-store";

interface ClusterShellLauncherProps {
  clusterId?: string;
  clusterName?: string;
  disabled?: boolean;
  disabledReason?: string;
}

/**
 * Top-level entry point for the audited cluster shell. The keyboard shortcut
 * and button intentionally share one action so both always focus the same
 * cluster-scoped drawer tab.
 */
export function ClusterShellLauncher({
  clusterId,
  clusterName,
  disabled = false,
  disabledReason,
}: ClusterShellLauncherProps) {
  const available = Boolean(clusterId) && !disabled;
  const label = clusterName || clusterId || "active cluster";
  const openShell = useCallback(() => {
    if (!clusterId || !available) return;
    openClusterShellWindow(clusterId, clusterName);
  }, [available, clusterId, clusterName]);

  useEffect(() => {
    if (!available) return;
    const handleShortcut = (event: KeyboardEvent) => {
      const isBackquote = event.key === "`" || event.code === "Backquote";
      if (!(event.ctrlKey || event.metaKey) || !isBackquote) return;
      event.preventDefault();
      openShell();
    };
    window.addEventListener("keydown", handleShortcut);
    return () => window.removeEventListener("keydown", handleShortcut);
  }, [available, openShell]);

  if (!clusterId) return null;

  const title = disabled
    ? disabledReason || `Cluster shell is unavailable for ${label}`
    : `Open cluster shell for ${label} (Ctrl+\`)`;

  return (
    <button
      type="button"
      onClick={openShell}
      disabled={disabled}
      aria-label={title}
      title={title}
      className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
    >
      <TerminalSquare className="h-3.5 w-3.5" />
      <span className="hidden xl:inline">Shell</span>
      <kbd className="hidden font-mono text-[10px] 2xl:inline">Ctrl+`</kbd>
    </button>
  );
}
