import { useEffect } from "react";
import { Search } from "lucide-react";
import { useUIStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { BARE_BUTTON } from "@/lib/bare-button";

const openPalette = () => useUIStore.getState().setCommandPaletteOpen(true);

/**
 * GlobalSearch is the topbar search field. It is a button styled as a field:
 * clicking it (or pressing "/") opens the command palette, which lists
 * pages, clusters and cross-cluster resource searches. Cmd/Ctrl+K opens the
 * same palette (command-palette.tsx owns that shortcut); the kbd hints are
 * visual only so they stay clear of axe's touch-target-size checks.
 */
export function GlobalSearch() {
  // "/" opens the palette unless the user is already typing in a form control.
  useEffect(() => {
    function onKeydown(e: KeyboardEvent) {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const active = document.activeElement;
      const isTypingTarget =
        active instanceof HTMLInputElement ||
        active instanceof HTMLTextAreaElement ||
        active instanceof HTMLSelectElement ||
        active?.getAttribute("contenteditable") === "true";
      if (!isTypingTarget) {
        e.preventDefault();
        openPalette();
      }
    }
    document.addEventListener("keydown", onKeydown);
    return () => document.removeEventListener("keydown", onKeydown);
  }, []);

  return (
    <ActionButton
      {...BARE_BUTTON}
      onClick={openPalette}
      aria-label="Search resources"
      aria-haspopup="dialog"
      className={cn(
        "relative flex h-8 w-full items-center gap-2 rounded-md border border-border bg-background pl-8 pr-16 text-left text-sm",
        "text-muted-foreground transition-colors hover:bg-accent/50",
        "focus:outline-hidden focus:ring-1 focus:ring-ring focus:border-ring font-normal whitespace-normal shrink",
      )}
    >
      <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2" />
      <span className="truncate">Search resources...</span>
      <span className="pointer-events-none absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1">
        <kbd className="hidden items-center rounded-sm border border-border px-1.5 py-0.5 font-mono text-[10px] md:inline-flex">
          /
        </kbd>
        <kbd className="hidden items-center rounded-sm border border-border px-1.5 py-0.5 font-mono text-[10px] sm:inline-flex">
          ⌘K
        </kbd>
      </span>
    </ActionButton>
  );
}
