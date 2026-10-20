import { useEffect } from "react";
import { useUIStore } from "@/lib/store";
import { SearchTrigger } from "@/ui";

const openPalette = () => useUIStore.getState().setCommandPaletteOpen(true);

/**
 * "/" opens the command palette unless the user is already typing in a form
 * control. Cmd/Ctrl+K opens the same palette (command-palette.tsx owns that
 * shortcut) — there is exactly one search affordance in the product.
 */
export function useSlashToOpenPalette() {
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
}

/** The header search field (a button styled as a field). */
export function GlobalSearch() {
  useSlashToOpenPalette();
  return (
    <SearchTrigger
      onOpen={openPalette}
      placeholder="Search pages, clusters and resources"
    />
  );
}
