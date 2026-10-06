import { X } from "lucide-react";

export interface FilterChip {
  /** Stable id: `<columnId>=<value>` or `search`. */
  id: string;
  label: string;
  value: string;
}

/** One chip per active faceted value, plus the search term when present. */
export function buildFilterChips(
  columnFilters: ReadonlyArray<{ id: string; value: unknown }>,
  labelFor: (columnId: string) => string,
  search: string,
): FilterChip[] {
  const chips: FilterChip[] = [];
  for (const filter of columnFilters) {
    if (!Array.isArray(filter.value)) continue;
    for (const value of filter.value) {
      chips.push({
        id: `${filter.id}=${String(value)}`,
        label: labelFor(filter.id),
        value: String(value),
      });
    }
  }
  if (search.trim()) {
    chips.push({ id: "search", label: "Search", value: search.trim() });
  }
  return chips;
}

/** Column filter state with one value removed (the column is dropped when empty). */
export function removeFilterValue(
  columnFilters: ReadonlyArray<{ id: string; value: unknown }>,
  columnId: string,
  value: string,
): Array<{ id: string; value: unknown }> {
  return columnFilters.flatMap((filter) => {
    if (filter.id !== columnId || !Array.isArray(filter.value)) return [filter];
    const rest = filter.value.filter((v) => String(v) !== value);
    return rest.length ? [{ id: filter.id, value: rest }] : [];
  });
}

export function DataTableFilterChips({
  chips,
  onRemove,
  onClearAll,
}: {
  chips: FilterChip[];
  onRemove: (chip: FilterChip) => void;
  onClearAll: () => void;
}) {
  if (chips.length === 0) return null;
  return (
    <div
      role="group"
      aria-label="Active filters"
      className="flex flex-wrap items-center gap-2"
    >
      {chips.map((chip) => (
        <span
          key={chip.id}
          className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/50 py-0.5 pl-2.5 pr-1 text-xs"
        >
          <span className="text-muted-foreground">{chip.label}:</span>
          <span className="font-medium">{chip.value}</span>
          <button
            type="button"
            aria-label={`Remove filter ${chip.label}: ${chip.value}`}
            onClick={() => onRemove(chip)}
            className="inline-flex h-5 w-5 items-center justify-center rounded-full text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      ))}
      <button
        type="button"
        onClick={onClearAll}
        className="rounded-sm px-1.5 py-0.5 text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
      >
        Clear all
      </button>
    </div>
  );
}
