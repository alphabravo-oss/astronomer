import { lazy, Suspense, useEffect } from "react";
import { useUIStore } from "@/lib/store";

const CommandPaletteDialog = lazy(() =>
  import("./command-palette-dialog").then((module) => ({
    default: module.CommandPaletteDialog,
  })),
);

export function CommandPalette() {
  const open = useUIStore((state) => state.commandPaletteOpen);
  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      const state = useUIStore.getState();
      if ((event.metaKey || event.ctrlKey) && event.key === "k") {
        event.preventDefault();
        state.setCommandPaletteOpen(!state.commandPaletteOpen);
      } else if (event.key === "Escape") {
        state.setCommandPaletteOpen(false);
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  return open ? (
    <Suspense fallback={<span role="status">Loading command palette…</span>}>
      <CommandPaletteDialog />
    </Suspense>
  ) : null;
}
