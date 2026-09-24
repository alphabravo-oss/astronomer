import { createPortal } from "react-dom";
import { useEffect, useEffectEvent, useRef, type ReactNode } from "react";
import { cn } from "@/lib/utils";

type OverlayPlacement = "center" | "right";

interface OverlayShellProps {
  onClose: () => void;
  children: ReactNode;
  placement?: OverlayPlacement;
  rootClassName?: string;
  backdropClassName?: string;
  closeOnBackdrop?: boolean;
}

interface OverlayBackdropProps {
  onClose: () => void;
  className?: string;
  ariaLabel?: string;
}

const placementClass: Record<OverlayPlacement, string> = {
  center: "items-center justify-center",
  right: "justify-end",
};

const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "textarea:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

function getFocusable(container: HTMLElement) {
  return Array.from(
    container.querySelectorAll<HTMLElement>(focusableSelector),
  ).filter((element) => !element.getAttribute("aria-hidden"));
}

export function OverlayBackdrop({
  onClose,
  className,
  ariaLabel = "Close overlay",
}: OverlayBackdropProps) {
  return (
    <button
      type="button"
      aria-label={ariaLabel}
      className={cn(
        "fixed inset-0 z-40 border-0 bg-black/50 p-0 backdrop-blur-xs",
        className,
      )}
      onClick={onClose}
    />
  );
}

export function OverlayShell({
  onClose,
  children,
  placement = "center",
  rootClassName,
  backdropClassName,
  closeOnBackdrop = true,
}: OverlayShellProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  // Keep the native listener registered through synchronous parent updates
  // from other global key handlers, while always calling the latest callback.
  const close = useEffectEvent(onClose);

  useEffect(() => {
    const previousActive =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const root = rootRef.current;
    if (!root) return;

    const alreadyFocused =
      document.activeElement instanceof HTMLElement &&
      root.contains(document.activeElement) &&
      document.activeElement !== root;
    if (!alreadyFocused) {
      // Native autofocus can move focus before the dialog and its focus trap
      // are mounted. A managed marker lets the overlay establish focus in one
      // deterministic effect, after semantics and restoration state exist.
      const managedInitial = root.querySelector<HTMLElement>(
        '[data-initial-focus="true"]',
      );
      const focusTarget = managedInitial ?? getFocusable(root)[0] ?? root;
      focusTarget.focus({ preventScroll: true });
    }

    return () => {
      if (previousActive && document.contains(previousActive)) {
        previousActive.focus({ preventScroll: true });
      }
    };
  }, []);

  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      const root = rootRef.current;
      // Nested pickers must not dismiss or move focus in the parent form.
      const overlays = document.querySelectorAll("[data-overlay-root]");
      if (
        !root ||
        e.defaultPrevented ||
        overlays.item(overlays.length - 1) !== root
      )
        return;
      if (e.key === "Escape") {
        e.preventDefault();
        close();
        return;
      }
      if (e.key !== "Tab") return;

      const focusable = getFocusable(root);
      if (focusable.length === 0) {
        e.preventDefault();
        root.focus({ preventScroll: true });
        return;
      }

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;

      if (
        e.shiftKey &&
        (!active || active === first || !root.contains(active))
      ) {
        e.preventDefault();
        last.focus({ preventScroll: true });
        return;
      }

      if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus({ preventScroll: true });
      }
    }
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, []);

  return createPortal(
    <div
      ref={rootRef}
      data-overlay-root
      onKeyDownCapture={(event) => {
        // A search field in a nested picker may inherit the parent's form.
        // Enter must not implicitly submit that form while the picker is open.
        const target = event.target;
        if (
          event.key === "Enter" &&
          target instanceof HTMLInputElement &&
          target.form &&
          !event.currentTarget.contains(target.form)
        )
          event.preventDefault();
      }}
      tabIndex={-1}
      className={cn(
        "fixed inset-0 z-overlay flex",
        placementClass[placement],
        rootClassName,
      )}
    >
      <button
        type="button"
        aria-label="Close overlay"
        aria-hidden="true"
        tabIndex={-1}
        className={cn(
          "absolute inset-0 border-0 bg-black/50 p-0 backdrop-blur-xs",
          backdropClassName,
        )}
        onClick={closeOnBackdrop ? onClose : undefined}
      />
      {children}
    </div>,
    document.body,
  );
}
