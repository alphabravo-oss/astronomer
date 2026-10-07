import type { ReactNode } from "react";
import type {
  ColumnFiltersState,
  PaginationState,
  SortingState,
} from "@tanstack/react-table";
import type { ColumnKind } from "@/components/ui/data-table-layout";
import type { TableEmptyState } from "@/components/ui/data-table-empty-state";

/**
 * Column sizing and overflow: declare a semantic `kind` first.
 *
 *   { key: "name",   header: "Name",   kind: "name",   accessor: ... }  // grows, truncates with a Tooltip
 *   { key: "state",  header: "State",  kind: "status", accessor: ... }  // never truncates
 *   { key: "ready",  header: "Ready",  kind: "count",  accessor: ... }  // right-aligned, tabular numerals
 *   { key: "uid",    header: "UID",    kind: "id",     accessor: ... }  // mono, middle-ellipsis, click copies
 *
 * Kinds: name | text | status | badge | count | percent | age | date |
 * version | id | bytes | actions | select. Each supplies default
 * size/minSize/maxSize, alignment and an overflow policy (see COLUMN_KINDS in
 * data-table-layout.ts). Explicit `width`, `size`, `minSize`, `maxSize`,
 * `align` and `grow` override the kind. A column with no kind and no width
 * keeps the legacy equal-share layout. At most one column should `grow`;
 * it absorbs the width the others leave. Header labels never wrap or clip:
 * the minimum width includes the label and sort icon.
 */
export interface Column<T> {
  key: string;
  header: string;
  /**
   * Full column name when `header` is a short visible label: screen readers,
   * the Columns menu and CSV export use it; the header tooltip falls back to it.
   */
  ariaLabel?: string;
  /** Tooltip shown on hover/focus of the header (defaults to `ariaLabel`). */
  headerTooltip?: ReactNode;
  /** Semantic column kind: the primary way to size and align a column. */
  kind?: ColumnKind;
  /** Preferred pixel width (overrides the kind's). */
  size?: number;
  minSize?: number;
  maxSize?: number;
  /** Absorb remaining table width. The `name` kind grows by default. */
  grow?: boolean;
  /** Default pin side. Row actions pin to the end; users can re-pin in the Columns menu. */
  pin?: "start" | "end" | false;
  accessor: (row: T) => React.ReactNode;
  /**
   * Plain text used by global search when the rendered cell is JSX. When it is
   * omitted, search falls back to `sortAccessor` and then the rendered value.
   */
  searchAccessor?: (row: T) => string;
  sortAccessor?: (row: T) => string | number;
  sortable?: boolean;
  filterable?: boolean;
  hidden?: boolean;
  /** Keep structural columns, such as row actions, visible and out of the selector. */
  hideable?: boolean;
  /** Render this as Rancher's dedicated three-dot row-action column. */
  rowActions?: boolean;
  width?: string;
  align?: "left" | "center" | "right";
  /** Allow multi-line cell content. Cells stay on one line by default. */
  wrap?: boolean;
  /**
   * When set, renders a faceted multi-select filter for this column in the
   * toolbar. The facet options are derived automatically from the column's
   * `sortAccessor` value (so faceted columns should define a `sortAccessor`
   * that returns the scalar to filter on).
   */
  filter?: { label?: string };
}

export interface DataTableProps<T> {
  /** Visual groups within the loaded page (or full virtualized row model). */
  groupBy?: (row: T) => string;
  data: T[];
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  density?: "compact" | "comfortable";
  searchable?: boolean;
  searchPlaceholder?: string;
  selectable?: boolean | ((row: T) => boolean);
  onRowClick?: (row: T) => void;
  onSelectionChange?: (selected: T[]) => void;
  bulkActions?: (selected: T[]) => ReactNode;
  pageSize?: number;
  loadingRows?: number;
  emptyState?: TableEmptyState;
  /** Include filters applied by the caller before rows reach this table. */
  filtersActive?: boolean;
  onClearFilters?: () => void;
  loading?: boolean;
  /**
   * When true, the table renders a single distinct error row (styled unlike the
   * empty state) instead of data/loading/empty. Pass `query.isError` here.
   */
  isError?: boolean;
  /** Raw query error, used to distinguish permission, offline, and API errors. */
  error?: unknown;
  /** Permission displayed for 401/403 table failures. */
  permission?: string;
  /** Message shown in the error row. */
  errorMessage?: string;
  /** Optional retry action; when provided, an inline Retry button is shown. */
  onRetry?: () => void;
  toolbar?: ReactNode;
  /**
   * Render an expandable detail row under each data row. Adds a leading
   * expander column; the row's expanded state survives data refreshes.
   * Return nothing for a row to hide its expander.
   *
   * Pods use it for containers (`PodContainersSubRow`, data already in the
   * list). A Deployment -> ReplicaSets sub row follows the same shape, but the
   * ReplicaSets are not part of the deployments list payload, so load them
   * inside the sub row component (lazy: it only mounts when expanded):
   *
   *   renderSubRow={(deployment) => <ReplicaSetsSubRow deployment={deployment} />}
   */
  renderSubRow?: (row: T) => ReactNode;
  /**
   * Add an "Export CSV" toolbar action that downloads the filtered rows and
   * visible columns in display order. On server-paged tables only the
   * current page is exported, and the action says so.
   */
  exportCsv?: boolean | { filename?: string };
  /**
   * Show the saved-Views menu. Defaults to on when `persistKey` is set and
   * the table is inside a query client (saved views persist per user).
   */
  savedViews?: boolean;
  /**
   * Row keyboard navigation: j/k or arrows move focus, Enter opens
   * (onRowClick), x toggles selection, / focuses search. Default on.
   */
  keyboardNav?: boolean;
  className?: string;
  /**
   * When set, the user's column-visibility choices are persisted to
   * localStorage under this key and restored on next mount. Omit for
   * ephemeral (non-persisted) tables.
   */
  persistKey?: string;
  /**
   * Opt into interactive column resizing. When false (the default), the table
   * renders identically to before — fixed widths come from each column's
   * `width`. When true, columns become drag-resizable and their pixel sizes are
   * persisted under `dt:<persistKey>:sizing` if `persistKey` is set.
   */
  resizable?: boolean;
  /**
   * Choose row virtualization for client-side datasets. `"auto"` (the
   * default) switches at 250 loaded rows; `false` retains paginated semantic
   * table markup; `true` always renders a virtualized grid. Server-paged
   * tables always retain server pagination, even if this is `true`.
   *
   * Virtualized tables render a
   * DIV-based ARIA grid that windows the *full* filtered+sorted row model
   * (pagination is disabled and the pagination footer is hidden); only the
   * rows in (and near) the viewport are mounted. Search/sort/faceted-filter/
   * selection still apply over the full row model.
   *
   */
  virtualized?: boolean | "auto";
  /**
   * Fit columns within the page by default. Horizontal scrolling is an
   * explicit choice for detail tables that must preserve full-width values.
   */
  layout?: "fit" | "scroll";
  /**
   * Opt into server-driven pagination. `data` should hold only the current
   * page's rows; the table will not slice further. The caller owns the
   * pagination state and feeds it into its query params so each page is a
   * separate fetch. (Search/sort remain client-side over the loaded page —
   * pass `searchable={false}` if that's misleading for the dataset.)
   */
  serverSide?: {
    rowCount: number;
    rowCountIsLowerBound?: boolean;
    rowCountIsUnknown?: boolean;
    pagination: PaginationState;
    onPaginationChange: (next: PaginationState) => void;
    /**
     * Controlled search for APIs that filter before pagination. When present,
     * the toolbar delegates to the caller and does not filter the current page
     * a second time.
     */
    search?: {
      value: string;
      onChange: (value: string) => void;
    };
    /** Controlled sorting over the full server dataset. */
    sorting?: {
      value: SortingState;
      onChange: (next: SortingState) => void;
    };
    /**
     * Controlled faceted column filters (`manualFiltering`) for endpoints that
     * filter before pagination. Facet option lists still come from the loaded
     * page, so only use this where the API accepts the matching query params.
     */
    filtering?: {
      value: ColumnFiltersState;
      onChange: (next: ColumnFiltersState) => void;
    };
  };
}
