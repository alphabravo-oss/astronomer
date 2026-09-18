/**
 * Search indexing for DataTable's client-side global filter.
 *
 * React Table invokes the filter predicate once per row for every new query.
 * Accessors are allowed to format values or build JSX, so evaluating them from
 * that hot predicate turns a 5k-row search into repeated render work. Build
 * the normalized, visible-cell text once per data/column change instead.
 */
export interface SearchableColumn<T> {
  key: string;
  accessor: (row: T) => unknown;
  searchAccessor?: (row: T) => string;
  sortAccessor?: (row: T) => string | number;
}

export const SEARCH_INDEX_SEPARATOR = "\u001f";

export function normalizeSearchText(value: unknown): string {
  return String(value ?? "")
    .normalize("NFKD")
    .replace(/\p{Diacritic}/gu, "")
    .toLocaleLowerCase()
    .trim();
}

function searchableValue<T>(column: SearchableColumn<T>, row: T): unknown {
  if (column.searchAccessor) return column.searchAccessor(row);
  if (column.sortAccessor) return column.sortAccessor(row);
  return column.accessor(row);
}

/**
 * Returns one normalized haystack per row ID. Only visible columns participate,
 * matching the table's column-toggle search contract.
 */
export function buildSearchIndex<T>(
  data: readonly T[],
  columns: readonly SearchableColumn<T>[],
  isVisible: (column: SearchableColumn<T>) => boolean,
  keyExtractor: (row: T) => string,
): ReadonlyMap<string, string> {
  const visibleColumns = columns.filter(isVisible);
  return new Map(
    data.map((row) => [
      keyExtractor(row),
      visibleColumns
        .map((column) => normalizeSearchText(searchableValue(column, row)))
        .filter(Boolean)
        .join(SEARCH_INDEX_SEPARATOR),
    ]),
  );
}

export function searchIndexMatches(
  index: ReadonlyMap<string, string>,
  rowID: string,
  query: unknown,
): boolean {
  const normalizedQuery = normalizeSearchText(query);
  return !normalizedQuery || index.get(rowID)?.includes(normalizedQuery) === true;
}
