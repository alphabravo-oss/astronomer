import type { ReactNode } from "react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { OverlayShell } from "@/components/ui/overlay-shell";
import { DialogTitle } from "@/components/ui/dialog";

interface DrawerShellProps {
  title: string;
  onClose: () => void;
  children: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  panelClassName?: string;
  bodyClassName?: string;
}

export function DrawerShell({
  title,
  onClose,
  children,
  subtitle,
  actions,
  panelClassName,
  bodyClassName,
}: DrawerShellProps) {
  // OverlayShell portals to the document root, so page animations and sticky
  // toolbars cannot trap the drawer in a stacking context.
  return (
    <OverlayShell
      onClose={onClose}
      dialog
      placement="right"
      backdropClassName="bg-black/40 backdrop-blur-0"
    >
      <div
        className={cn(
          "relative flex h-full w-full max-w-2xl flex-col border-l border-border bg-background shadow-xl",
          panelClassName,
        )}
      >
        <div className="flex items-center justify-between gap-4 border-b border-border px-5 py-4">
          <div className="min-w-0">
            <DialogTitle className="truncate text-base font-semibold text-foreground">
              {title}
            </DialogTitle>
            {subtitle && (
              <div className="mt-1 text-xs text-muted-foreground">
                {subtitle}
              </div>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {actions}
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>
        <div
          className={cn("min-h-0 flex-1 overflow-y-auto p-5", bodyClassName)}
        >
          {children}
        </div>
      </div>
    </OverlayShell>
  );
}
