import { useEffect, useRef, useState } from "react";
import { useQuery, type QueryKey } from "@tanstack/react-query";
import { Select } from "./select";
import { ActionButton } from "./action-button";
import { QueryStates } from "./query-states";
import type { PaginatedResponse } from "@/types";

export type SelectPage = {
  limit: number;
  offset: number;
};

/** Bounded server pages, including empty eligible pages and nonuniform offsets.
 * Key this control by its authorization/parent scope to reset pages and selection.
 */
export function PagedSelect<T extends { id: string }>({
  label,
  id,
  name,
  value,
  defaultValue = "",
  onChange,
  required,
  disabled,
  queryKey,
  fetchPage,
  optionLabel,
  eligible = () => true,
  permission,
  placeholder = "Select an option…",
  onValidityChange,
}: {
  label: string;
  id?: string;
  name?: string;
  value?: string;
  defaultValue?: string;
  onChange?: (id: string) => void;
  required?: boolean;
  disabled?: boolean;
  queryKey: (params: SelectPage) => QueryKey;
  fetchPage: (
    params: SelectPage,
    signal: AbortSignal,
  ) => Promise<PaginatedResponse<T>>;
  optionLabel: (row: T) => string;
  eligible?: (row: T) => boolean;
  permission: string;
  placeholder?: string;
  onValidityChange?: (valid: boolean) => void;
}) {
  const [offsets, setOffsets] = useState([0]);
  const [selection, setSelection] = useState(defaultValue);
  const selected = value ?? selection;
  const selectRef = useRef<HTMLSelectElement>(null);
  const offset = offsets[offsets.length - 1];
  const params = { limit: 25, offset };
  const query = useQuery({
    queryKey: queryKey(params),
    queryFn: ({ signal }) => fetchPage(params, signal),
    enabled: !disabled,
    throwOnError: false,
  });
  const rows =
    disabled || query.isError ? [] : (query.data?.data ?? []).filter(eligible);
  const next = query.data?.pagination.next_offset;
  // A background refresh must not swallow a click on a visible page by
  // disabling the button between pointer-down and pointer-up.
  const canNext =
    !query.isError &&
    query.data?.pagination.has_more &&
    next != null &&
    next > offset;
  const selectedRow = query.data?.data.find((row) => row.id === selected);
  const ineligible = !!selectedRow && !eligible(selectedRow);
  const validation = query.isError
    ? "Choices could not be verified. Retry or return to a previous page."
    : query.isLoading
      ? "Wait for the choices to load."
      : ineligible
        ? "The selected option is no longer eligible."
        : "";
  useEffect(() => {
    selectRef.current?.setCustomValidity(disabled ? "" : validation);
    onValidityChange?.(!disabled && !validation && (!required || !!selected));
  }, [disabled, validation, required, selected, onValidityChange]);
  return (
    <div className="space-y-2">
      <Select
        id={id}
        ref={selectRef}
        name={name}
        aria-label={label}
        value={selected}
        required={required}
        disabled={disabled}
        onChange={(event) => {
          setSelection(event.target.value);
          onChange?.(event.target.value);
        }}
      >
        <option value="">{placeholder}</option>
        {selected && !rows.some((row) => row.id === selected) && (
          <option value={selected}>{selected}</option>
        )}
        {rows.map((row) => (
          <option key={row.id} value={row.id}>
            {optionLabel(row)}
          </option>
        ))}
      </Select>
      {!disabled && (
        <>
          <QueryStates
            query={query}
            permission={permission}
            loadingTitle={`Loading ${label.toLowerCase()}`}
            errorTitle={`Could not load ${label.toLowerCase()}`}
          >
            {rows.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No eligible options on this page.
              </p>
            ) : null}
          </QueryStates>
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <ActionButton
              size="sm"
              aria-label={`Previous ${label.toLowerCase()} page`}
              disabled={offsets.length === 1}
              onClick={() => setOffsets((pages) => pages.slice(0, -1))}
            >
              Previous
            </ActionButton>
            <span>Page {offsets.length}</span>
            <ActionButton
              size="sm"
              aria-label={`Next ${label.toLowerCase()} page`}
              disabled={!canNext}
              onClick={() => {
                if (canNext && next != null)
                  setOffsets((pages) => [...pages, next]);
              }}
            >
              Next
            </ActionButton>
          </div>
        </>
      )}
    </div>
  );
}
