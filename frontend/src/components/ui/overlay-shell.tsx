import {
  useEffectEvent,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { cn } from "@/lib/utils";
import {
  Dialog,
  DialogLayer,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";

type OverlayPlacement = "center" | "right";

interface OverlayShellProps {
  onClose: () => void;
  children: ReactNode;
  placement?: OverlayPlacement;
  rootClassName?: string;
  backdropClassName?: string;
  closeOnBackdrop?: boolean;
  /**
   * When true the wrapper is the labelled `role="dialog"`; a descendant
   * `DialogTitle` supplies the name (ModalShell/DrawerShell). Otherwise the
   * wrapper is presentational and children own their semantics.
   */
  dialog?: boolean;
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

// Escape events Radix routed to the topmost overlay. Radix listens in the
// capture phase and would close before earlier bubble-phase app handlers have
// run; we claim the event and close from the bubble phase instead, preserving
// the legacy ordering and honoring defaultPrevented from inner widgets.
const claimedEscapes = new WeakSet<Event>();

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

/**
 * Radix Dialog supplies the focus trap, scroll lock, Escape layering, aria
 * hiding of the page behind and portaling. This shell keeps its legacy
 * contract: managed initial focus, synchronous focus return on unmount, and
 * close only through `onClose` (backdrop click or Escape).
 */
export function OverlayShell({
  onClose,
  children,
  placement = "center",
  rootClassName,
  backdropClassName,
  closeOnBackdrop = true,
  dialog = false,
}: OverlayShellProps) {
  // Keep the callback fresh across synchronous parent updates.
  const close = useEffectEvent(onClose);
  // Captured during the first render, before Radix moves focus into the layer.
  const [previousActive] = useState(() =>
    typeof document !== "undefined" &&
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  );

  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      const root = rootRef.current;
      const overlays = document.querySelectorAll("[data-overlay-root]");
      if (
        !root ||
        e.key !== "Escape" ||
        !claimedEscapes.has(e) ||
        overlays.item(overlays.length - 1) !== root
      )
        return;
      close();
    }
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, []);

  useEffect(
    () => () => {
      if (previousActive && document.contains(previousActive)) {
        previousActive.focus({ preventScroll: true });
      }
    },
    [previousActive],
  );

  return (
    <Dialog open>
      <DialogPortal>
        <DialogLayer
          ref={rootRef}
          data-overlay-root
          onEscapeKeyDown={(event) => {
            // Radix only calls this on the topmost layer.
            claimedEscapes.add(event);
            event.preventDefault();
          }}
          {...(dialog ? {} : { role: "presentation" })}
          aria-describedby={undefined}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            const root = event.currentTarget as HTMLElement | null;
            if (!root) return;
            if (
              document.activeElement instanceof HTMLElement &&
              root.contains(document.activeElement) &&
              document.activeElement !== root
            )
              return;
            // A managed marker lets the overlay establish focus in one
            // deterministic step instead of racing native autofocus.
            const target =
              root.querySelector<HTMLElement>('[data-initial-focus="true"]') ??
              Array.from(
                root.querySelectorAll<HTMLElement>(focusableSelector),
              ).find((el) => !el.getAttribute("aria-hidden")) ??
              root;
            target.focus({ preventScroll: true });
          }}
          // Focus is restored synchronously on unmount above.
          onCloseAutoFocus={(event) => event.preventDefault()}
          // Only the backdrop and Escape dismiss; portaled toasts or nested
          // pickers outside this layer must not.
          onInteractOutside={(event) => event.preventDefault()}
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
          className={cn(
            "fixed inset-0 z-overlay flex outline-none",
            placementClass[placement],
            rootClassName,
          )}
        >
          {dialog ? null : (
            <DialogTitle asChild>
              <span className="sr-only">Dialog</span>
            </DialogTitle>
          )}
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
        </DialogLayer>
      </DialogPortal>
    </Dialog>
  );
}
