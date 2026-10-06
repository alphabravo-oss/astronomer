import { Fragment, useRef, useState } from "react";
import { MoreVertical } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

export interface ActionMenuItem {
  label: string;
  icon?: React.ReactNode;
  onClick: () => void;
  variant?: "default" | "destructive";
  disabled?: boolean;
  disabledReason?: string;
  separator?: boolean;
}

interface ActionMenuProps {
  items: ActionMenuItem[];
  ariaLabel?: string;
}

// The menu portals to <body> (Radix), so overflow-hidden ancestors such as
// DataTable wrappers never clip it; Radix also handles collision flipping,
// Escape, focus return and type-ahead.
export function ActionMenu({
  items,
  ariaLabel = "Open actions menu",
}: ActionMenuProps) {
  const [open, setOpen] = useState(false);
  // Radix opens on pointerdown. A click with no preceding pointerdown comes
  // from assistive tech or a programmatic click, so it toggles instead.
  const pointerOpened = useRef(false);

  return (
    <DropdownMenu open={open} onOpenChange={setOpen} modal={false}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={ariaLabel}
          onPointerDown={() => {
            pointerOpened.current = true;
          }}
          onClick={(e) => {
            // Never let a row-level click handler see the trigger click.
            e.stopPropagation();
            if (pointerOpened.current) pointerOpened.current = false;
            else setOpen((o) => !o);
          }}
          className="inline-flex items-center justify-center h-7 w-7 rounded-sm
          text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          <MoreVertical className="h-4 w-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        className="w-52"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
      >
        {items.map((item, i) => (
          <Fragment key={i}>
            {item.separator && i > 0 && <DropdownMenuSeparator />}
            <DropdownMenuItem
              asChild
              disabled={item.disabled}
              variant={item.variant}
              onSelect={() => item.onClick()}
              className="whitespace-nowrap"
            >
              <button
                type="button"
                disabled={item.disabled}
                title={item.disabledReason}
              >
                {item.icon && <span className="shrink-0">{item.icon}</span>}
                {item.label}
              </button>
            </DropdownMenuItem>
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
