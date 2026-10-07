import { useId, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { controlClassName } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { useVirtualRows } from "@/components/ui/use-virtual-rows";

export interface ComboboxOption {
  value: string;
  label: string;
  description?: string;
}

export interface ComboboxProps {
  options: readonly ComboboxOption[];
  /** Controlled selection; `null`/`""` shows the placeholder. */
  value: string | null;
  onValueChange: (value: string) => void;
  placeholder?: string;
  searchPlaceholder?: string;
  emptyText?: string;
  /** Shows a busy row while options are being fetched. */
  loading?: boolean;
  /**
   * Async/server-side search. When set, options are shown as given (the
   * caller filters); otherwise they are filtered locally by label/description.
   */
  onSearchChange?: (query: string) => void;
  disabled?: boolean;
  id?: string;
  className?: string;
  "aria-label"?: string;
  "aria-labelledby"?: string;
}

/** Lists longer than this render through a windowed virtualizer. */
export const COMBOBOX_VIRTUALIZE_AFTER = 100;
const ROW_HEIGHT = 40;
const LIST_MAX_HEIGHT = 280;

export function Combobox({
  options,
  value,
  onValueChange,
  placeholder = "Select…",
  searchPlaceholder = "Search…",
  emptyText = "No results found",
  loading = false,
  onSearchChange,
  disabled = false,
  id,
  className,
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
}: ComboboxProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const listId = useId();
  const selected = options.find((o) => o.value === value);

  const handleOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) {
      setQuery("");
      onSearchChange?.("");
    }
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <button
          type="button"
          id={id}
          aria-label={ariaLabel}
          aria-labelledby={ariaLabelledBy}
          aria-haspopup="listbox"
          aria-expanded={open}
          aria-controls={open ? listId : undefined}
          disabled={disabled}
          className={cn(
            controlClassName,
            "flex items-center justify-between gap-2 text-left",
            !selected && "text-muted-foreground",
            className,
          )}
        >
          <span className="truncate">{selected?.label ?? placeholder}</span>
          <ChevronDown
            aria-hidden="true"
            className="h-4 w-4 shrink-0 text-muted-foreground"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-(--radix-popover-trigger-width) min-w-56 p-0"
      >
        <ComboboxPanel
          listId={listId}
          options={options}
          selectedValue={value}
          query={query}
          onQueryChange={(next) => {
            setQuery(next);
            onSearchChange?.(next);
          }}
          searchPlaceholder={searchPlaceholder}
          emptyText={emptyText}
          loading={loading}
          localFilter={!onSearchChange}
          ariaLabel={ariaLabel}
          onSelect={(next) => {
            onValueChange(next);
            handleOpenChange(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

function ComboboxPanel({
  listId,
  options,
  selectedValue,
  query,
  onQueryChange,
  searchPlaceholder,
  emptyText,
  loading,
  localFilter,
  ariaLabel,
  onSelect,
}: {
  listId: string;
  options: readonly ComboboxOption[];
  selectedValue: string | null;
  query: string;
  onQueryChange: (query: string) => void;
  searchPlaceholder: string;
  emptyText: string;
  loading: boolean;
  localFilter: boolean;
  ariaLabel?: string;
  onSelect: (value: string) => void;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [activeState, setActive] = useState(0);
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!localFilter || !q) return options;
    return options.filter(
      (o) =>
        o.label.toLowerCase().includes(q) ||
        o.value.toLowerCase().includes(q) ||
        o.description?.toLowerCase().includes(q),
    );
  }, [options, query, localFilter]);
  const active = Math.min(activeState, Math.max(visible.length - 1, 0));
  const virtualized = visible.length > COMBOBOX_VIRTUALIZE_AFTER;
  const rows = useVirtualRows({
    count: visible.length,
    estimateSize: ROW_HEIGHT,
    overscan: 8,
    scrollRef,
    enabled: virtualized,
  });
  const optionId = (index: number) => `${listId}-opt-${index}`;

  const move = (index: number) => {
    const next = Math.max(0, Math.min(visible.length - 1, index));
    setActive(next);
    if (virtualized) rows.scrollToIndex(next);
    else
      document.getElementById(optionId(next))?.scrollIntoView?.({
        block: "nearest",
      });
  };

  const renderOption = (option: ComboboxOption, index: number) => (
    // Keyboard selection is handled by the search input (aria-activedescendant).
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events
    <div
      key={option.value}
      tabIndex={-1}
      id={optionId(index)}
      role="option"
      aria-selected={option.value === selectedValue}
      data-active={index === active || undefined}
      // The input keeps focus; mouse selection must not blur it first.
      onMouseDown={(e) => e.preventDefault()}
      onMouseMove={() => index !== active && setActive(index)}
      onClick={() => onSelect(option.value)}
      className={cn(
        "flex cursor-default items-center gap-2 rounded-sm px-2.5 text-sm",
        "data-active:bg-accent",
      )}
      style={{ height: ROW_HEIGHT }}
    >
      <span className="min-w-0 flex-1">
        <span className="block truncate text-foreground">{option.label}</span>
        {option.description && (
          <span className="block truncate text-xs text-muted-foreground">
            {option.description}
          </span>
        )}
      </span>
      {option.value === selectedValue && (
        <Check aria-hidden="true" className="h-4 w-4 shrink-0" />
      )}
    </div>
  );

  return (
    <div>
      <input
        type="text"
        role="combobox"
        aria-label={ariaLabel ? `Search ${ariaLabel}` : "Search options"}
        aria-expanded="true"
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={
          visible.length > 0 &&
          (!virtualized || rows.items.some((r) => r.index === active))
            ? optionId(active)
            : undefined
        }
        value={query}
        placeholder={searchPlaceholder}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => {
          setActive(0);
          onQueryChange(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            move(active + 1);
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            move(active - 1);
          } else if (e.key === "Home") {
            e.preventDefault();
            move(0);
          } else if (e.key === "End") {
            e.preventDefault();
            move(visible.length - 1);
          } else if (e.key === "Enter") {
            e.preventDefault();
            const option = visible[active];
            if (option) onSelect(option.value);
          }
        }}
        className="h-9 w-full border-b border-border bg-transparent px-3 text-sm outline-none placeholder:text-muted-foreground"
      />
      <div
        ref={scrollRef}
        id={listId}
        role="listbox"
        aria-label={ariaLabel}
        aria-busy={loading || undefined}
        className="overflow-y-auto p-1"
        style={{ maxHeight: LIST_MAX_HEIGHT }}
      >
        {virtualized ? (
          <div style={{ height: rows.totalSize, position: "relative" }}>
            {rows.items.map((item) => (
              <div
                key={item.key}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  right: 0,
                  transform: `translateY(${item.start}px)`,
                }}
              >
                {renderOption(visible[item.index], item.index)}
              </div>
            ))}
          </div>
        ) : (
          visible.map(renderOption)
        )}
      </div>
      {loading && (
        <div
          role="status"
          className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground"
        >
          <Loader2 aria-hidden="true" className="h-3 w-3 animate-spin" />
          Loading…
        </div>
      )}
      {!loading && visible.length === 0 && (
        <div
          role="status"
          className="px-3 py-4 text-center text-xs text-muted-foreground"
        >
          {emptyText}
        </div>
      )}
    </div>
  );
}
