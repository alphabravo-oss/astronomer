import type { ComponentPropsWithoutRef } from "react";
import { DropdownMenu as MenuPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

export const DropdownMenu = MenuPrimitive.Root;
export const DropdownMenuTrigger = MenuPrimitive.Trigger;
export const DropdownMenuGroup = MenuPrimitive.Group;

export function DropdownMenuContent({
  className,
  sideOffset = 4,
  align = "end",
  ...props
}: ComponentPropsWithoutRef<typeof MenuPrimitive.Content>) {
  return (
    <MenuPrimitive.Portal>
      <MenuPrimitive.Content
        sideOffset={sideOffset}
        align={align}
        collisionPadding={8}
        className={cn(
          "z-popover min-w-32 overflow-y-auto rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg outline-none",
          "max-h-(--radix-dropdown-menu-content-available-height)",
          className,
        )}
        {...props}
      />
    </MenuPrimitive.Portal>
  );
}

export function DropdownMenuItem({
  className,
  variant = "default",
  ...props
}: ComponentPropsWithoutRef<typeof MenuPrimitive.Item> & {
  variant?: "default" | "destructive";
}) {
  return (
    <MenuPrimitive.Item
      className={cn(
        "flex w-full cursor-default select-none items-center gap-2 rounded-sm px-2.5 py-1.5 text-left text-xs outline-none transition-colors",
        "data-disabled:cursor-not-allowed data-disabled:opacity-50",
        variant === "destructive"
          ? "text-status-error data-highlighted:bg-status-error/10"
          : "text-popover-foreground data-highlighted:bg-accent",
        className,
      )}
      {...props}
    />
  );
}

export function DropdownMenuSeparator({
  className,
  ...props
}: ComponentPropsWithoutRef<typeof MenuPrimitive.Separator>) {
  return (
    <MenuPrimitive.Separator
      className={cn("-mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  );
}

export function DropdownMenuLabel({
  className,
  ...props
}: ComponentPropsWithoutRef<typeof MenuPrimitive.Label>) {
  return (
    <MenuPrimitive.Label
      className={cn(
        "px-2.5 py-1.5 text-xs font-medium text-muted-foreground",
        className,
      )}
      {...props}
    />
  );
}
