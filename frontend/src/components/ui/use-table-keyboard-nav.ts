import type { KeyboardEvent, RefObject } from "react";
import type { Row, RowData } from "@tanstack/react-table";
import type { DataTableFeatures } from "@/components/ui/data-table-features";

interface KeyboardNavOptions<T extends RowData> {
  keyboardNav: boolean;
  searchable: boolean;
  selectable: boolean | ((row: T) => boolean);
  searchRef: RefObject<HTMLInputElement | null>;
  wrapperRef: RefObject<HTMLDivElement | null>;
  rows: Row<DataTableFeatures, T>[];
  effectiveVirtualized: boolean;
  focusRowAt: (index: number) => void;
}

/** Row keyboard navigation: j/k or arrows move, x selects, / focuses search. */
export function useTableKeyboardNav<T extends RowData>({
  keyboardNav,
  searchable,
  selectable,
  searchRef,
  wrapperRef,
  rows,
  effectiveVirtualized,
  focusRowAt,
}: KeyboardNavOptions<T>) {
  return (event: KeyboardEvent<HTMLDivElement>) => {
    if (!keyboardNav || event.defaultPrevented) return;
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    const target = event.target as HTMLElement;
    if (
      target.closest(
        "input, textarea, select, [contenteditable=''], [contenteditable='true']",
      )
    ) {
      return;
    }
    const rowEl = target.closest<HTMLElement>("[data-row-index]");
    const onRow = rowEl !== null && target === rowEl;
    const onContainer = target.matches("[data-table-region], [role='grid']");
    if (!onRow && !onContainer) return;
    if (event.key === "/") {
      if (!searchable || !searchRef.current) return;
      event.preventDefault();
      searchRef.current.focus();
      return;
    }
    const index = onRow ? Number(rowEl.dataset.rowIndex) : -1;
    const step =
      event.key === "j" || (!effectiveVirtualized && event.key === "ArrowDown")
        ? 1
        : event.key === "k" ||
            (!effectiveVirtualized && event.key === "ArrowUp")
          ? -1
          : 0;
    if (step !== 0) {
      event.preventDefault();
      const next = Math.min(Math.max(index + step, 0), rows.length - 1);
      if (effectiveVirtualized) focusRowAt(next);
      else
        wrapperRef.current
          ?.querySelector<HTMLElement>(`[data-row-index="${next}"]`)
          ?.focus();
      return;
    }
    if (event.key === "x" && onRow && selectable) {
      const row = rows[index];
      if (row?.getCanSelect()) {
        event.preventDefault();
        row.toggleSelected();
      }
    }
  };
}
