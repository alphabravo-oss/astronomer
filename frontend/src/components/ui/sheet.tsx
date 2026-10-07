import type { ComponentPropsWithoutRef } from "react";
import { Dialog as DialogPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";
import { DialogOverlay } from "@/components/ui/dialog";

export {
  Dialog as Sheet,
  DialogTrigger as SheetTrigger,
  DialogClose as SheetClose,
  DialogTitle as SheetTitle,
  DialogDescription as SheetDescription,
} from "@/components/ui/dialog";

/** Edge-anchored panel (right by default) on the Radix Dialog primitives. */
export function SheetContent({
  side = "right",
  className,
  children,
  ...props
}: ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & {
  side?: "right" | "left";
}) {
  return (
    <DialogPrimitive.Portal>
      <DialogOverlay className="bg-black/40 backdrop-blur-0" />
      <DialogPrimitive.Content
        className={cn(
          "fixed inset-y-0 z-overlay flex h-full w-full max-w-2xl flex-col bg-background shadow-xl outline-none",
          side === "right"
            ? "right-0 border-l border-border"
            : "left-0 border-r border-border",
          className,
        )}
        {...props}
      >
        {children}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}
