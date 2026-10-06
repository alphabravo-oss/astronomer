import { columnFullName } from "@/components/ui/data-table-header-label";
import type { ReactNode, Ref } from "react";
import { useState } from "react";
import type {
  Column as RtColumn,
  Table as RtTable,
  RowData,
} from "@tanstack/react-table";
import {
  ArrowDown,
  ArrowLeftToLine,
  ArrowRightToLine,
  ArrowUp,
  Download,
  Filter,
  RotateCcw,
  Search,
  SlidersHorizontal,
  X,
} from "lucide-react";
import type { Column } from "@/components/ui/data-table";
import { Checkbox } from "@/components/ui/checkbox";
import type { ColumnPinningState } from "@/components/ui/data-table-layout";
import { Tooltip } from "@/components/ui/tooltip";
import type { DataTableFeatures } from "./data-table-features";
import { cn } from "@/lib/utils";

interface DataTableToolbarProps<T extends RowData> {
  table: RtTable<DataTableFeatures, T>;
  columns: Column<T>[];
  facetColumns: Column<T>[];
  searchable: boolean;
  searchPlaceholder: string;
  searchInput: string;
  onSearchInputChange: (value: string) => void;
  searchInputRef?: Ref<HTMLInputElement>;
  toolbar?: ReactNode;
  selectable: boolean | ((row: T) => boolean);
  selectedRows: T[];
  bulkActions?: (selected: T[]) => ReactNode;
  /** All columns in current display order (Columns menu order). */
  menuColumns?: Column<T>[];
  pinning?: ColumnPinningState;
  onMoveColumn?: (key: string, direction: -1 | 1) => void;
  onPinColumn?: (key: string, side: "start" | "end" | false) => void;
  onResetColumns?: () => void;
  onExportCsv?: () => void;
  /** Server-paged tables export only the page they hold. */
  exportPageOnly?: boolean;
  viewsMenu?: ReactNode;
}

export function DataTableToolbar<T extends RowData>({
  table,
  columns,
  facetColumns,
  searchable,
  searchPlaceholder,
  searchInput,
  onSearchInputChange,
  searchInputRef,
  toolbar,
  selectable,
  selectedRows,
  bulkActions,
  menuColumns,
  pinning,
  onMoveColumn,
  onPinColumn,
  onResetColumns,
  onExportCsv,
  exportPageOnly = false,
  viewsMenu,
}: DataTableToolbarProps<T>) {
  const [showColumnToggle, setShowColumnToggle] = useState(false);
  const listed = (menuColumns ?? columns).filter((definition) =>
    table.getColumn(definition.key)?.getCanHide(),
  );

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-1 flex-wrap items-center gap-2">
          {searchable && (
            <div className="relative max-w-sm flex-1">
              <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <input
                ref={searchInputRef}
                data-table-search=""
                type="text"
                placeholder={searchPlaceholder}
                value={searchInput}
                onChange={(event) => onSearchInputChange(event.target.value)}
                className="h-9 w-full rounded-md border border-border bg-background pl-9 pr-8 text-sm
                  placeholder:text-muted-foreground focus:outline-hidden focus:ring-1 focus:ring-ring"
              />
              {searchInput && (
                <button
                  type="button"
                  aria-label="Clear search"
                  onClick={() => onSearchInputChange("")}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          )}
          {facetColumns.map((definition) => {
            const column = table.getColumn(definition.key);
            if (!column) return null;
            return (
              <FacetedFilter
                key={definition.key}
                column={column}
                label={definition.filter?.label ?? columnFullName(definition)}
                onChange={() => table.setPageIndex(0)}
              />
            );
          })}
          {toolbar}
        </div>

        <div className="flex items-center gap-2 self-end sm:self-auto">
          {viewsMenu}
          {onExportCsv && (
            <Tooltip
              content={
                exportPageOnly
                  ? "Exports only the rows on this page"
                  : "Exports the filtered rows and visible columns"
              }
            >
              <button
                type="button"
                onClick={onExportCsv}
                className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3
                  text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              >
                <Download className="h-4 w-4" />
                {exportPageOnly ? "Export page (CSV)" : "Export CSV"}
              </button>
            </Tooltip>
          )}
          <div className="relative">
            <button
              type="button"
              aria-expanded={showColumnToggle}
              onClick={() => setShowColumnToggle((visible) => !visible)}
              className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3
              text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <SlidersHorizontal className="h-4 w-4" />
              Columns
            </button>

            {showColumnToggle && (
              <div className="absolute right-0 top-full z-50 mt-1 w-64 rounded-md border border-border bg-popover p-1 shadow-lg">
                {listed.map((definition, index) => {
                  const column = table.getColumn(definition.key);
                  if (!column) return null;
                  const isVisible = column.getIsVisible();
                  const side = pinning?.start.includes(definition.key)
                    ? "start"
                    : pinning?.end.includes(definition.key)
                      ? "end"
                      : false;
                  const controls = onMoveColumn && onPinColumn;
                  return (
                    <div
                      key={definition.key}
                      className="flex items-center gap-1 rounded-sm px-1 hover:bg-accent"
                    >
                      <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 px-1 py-1.5 text-sm">
                        <Checkbox
                          checked={isVisible}
                          onChange={() => column.toggleVisibility()}
                        />
                        <span className="truncate">
                          {columnFullName(definition)}
                        </span>
                      </label>
                      {controls && (
                        <>
                          <MenuIconButton
                            label={`Move ${columnFullName(definition)} up`}
                            disabled={index === 0 || side !== false}
                            onClick={() => onMoveColumn(definition.key, -1)}
                          >
                            <ArrowUp className="h-3.5 w-3.5" />
                          </MenuIconButton>
                          <MenuIconButton
                            label={`Move ${columnFullName(definition)} down`}
                            disabled={
                              index === listed.length - 1 || side !== false
                            }
                            onClick={() => onMoveColumn(definition.key, 1)}
                          >
                            <ArrowDown className="h-3.5 w-3.5" />
                          </MenuIconButton>
                          <MenuIconButton
                            label={`Pin ${columnFullName(definition)} left`}
                            pressed={side === "start"}
                            onClick={() =>
                              onPinColumn(
                                definition.key,
                                side === "start" ? false : "start",
                              )
                            }
                          >
                            <ArrowLeftToLine className="h-3.5 w-3.5" />
                          </MenuIconButton>
                          <MenuIconButton
                            label={`Pin ${columnFullName(definition)} right`}
                            pressed={side === "end"}
                            onClick={() =>
                              onPinColumn(
                                definition.key,
                                side === "end" ? false : "end",
                              )
                            }
                          >
                            <ArrowRightToLine className="h-3.5 w-3.5" />
                          </MenuIconButton>
                        </>
                      )}
                    </div>
                  );
                })}
                {onResetColumns && (
                  <button
                    type="button"
                    onClick={onResetColumns}
                    className="mt-1 flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                    Reset order and pinning
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {selectable && selectedRows.length > 0 && bulkActions ? (
        <div
          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm"
          aria-live="polite"
        >
          <span className="text-muted-foreground">
            {selectedRows.length} {selectedRows.length === 1 ? "row" : "rows"}{" "}
            selected
          </span>
          <div className="flex flex-wrap items-center gap-2">
            {bulkActions(selectedRows)}
          </div>
        </div>
      ) : null}
    </>
  );
}

function MenuIconButton({
  label,
  onClick,
  disabled,
  pressed,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  pressed?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground",
        "hover:bg-background hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
        "disabled:cursor-not-allowed disabled:opacity-30",
        pressed && "bg-primary/15 text-foreground",
      )}
    >
      {children}
    </button>
  );
}

function FacetedFilter<T extends RowData>({
  column,
  label,
  onChange,
}: {
  column: RtColumn<DataTableFeatures, T, unknown>;
  label: string;
  onChange?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const selected = (column.getFilterValue() as string[] | undefined) ?? [];
  const options = Array.from(column.getFacetedUniqueValues().keys())
    .map((value) => String(value))
    .filter((value) => value !== "")
    .sort();

  const apply = (next: string[]) => {
    column.setFilterValue(next.length ? next : undefined);
    onChange?.();
  };
  const toggle = (value: string) =>
    apply(
      selected.includes(value)
        ? selected.filter((candidate) => candidate !== value)
        : [...selected, value],
    );

  return (
    <div className="relative">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((visible) => !visible)}
        className={cn(
          "inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3 text-sm transition-colors",
          selected.length > 0
            ? "border-primary/50 bg-accent text-foreground"
            : "text-muted-foreground hover:bg-accent hover:text-foreground",
        )}
      >
        <Filter className="h-3.5 w-3.5" />
        {label}
        {selected.length > 0 && (
          <span className="ml-0.5 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1 text-2xs text-primary-foreground">
            {selected.length}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 w-52 rounded-md border border-border bg-popover p-1 shadow-lg">
          {options.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-muted-foreground">
              No values
            </p>
          ) : (
            options.map((option) => (
              <label
                key={option}
                className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm hover:bg-accent"
              >
                <Checkbox
                  checked={selected.includes(option)}
                  onChange={() => toggle(option)}
                />
                {option}
              </label>
            ))
          )}
          {selected.length > 0 && (
            <button
              type="button"
              onClick={() => apply([])}
              className="mt-1 w-full rounded-sm px-2 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              Clear filter
            </button>
          )}
        </div>
      )}
    </div>
  );
}
