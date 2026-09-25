import { useEffect, useRef, useState } from "react";

export function useHeaderPopover() {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const container = root.current;
    const trigger = container?.querySelector<HTMLButtonElement>("button");
    container
      ?.querySelector<HTMLElement>("[data-header-popover] button")
      ?.focus();
    const dismiss = (event: PointerEvent) => {
      if (!container?.contains(event.target as Node)) setOpen(false);
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setOpen(false);
        trigger?.focus();
      }
    };
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", key);
    };
  }, [open]);
  return { open, setOpen, root };
}
