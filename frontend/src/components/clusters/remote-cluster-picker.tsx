import { useVirtualizer } from "@tanstack/react-virtual";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { Check, ChevronsUpDown, Loader2, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { useCluster } from "@/lib/hooks/clusters";
import { useClusterSearch } from "@/lib/hooks/cluster-search";
import { cn } from "@/lib/utils";
import type { Cluster } from "@/types";

interface RemoteClusterPickerProps {
  value: string;
  onChange: (clusterId: string) => void;
  onBlur?: () => void;
  id?: string;
  name?: string;
  ariaLabel?: string;
  placeholder?: string;
  allowedClusterIds?: readonly string[];
  excludedClusterIds?: readonly string[];
  disabled?: boolean;
  className?: string;
}

function clusterLabel(cluster: Cluster): string {
  return cluster.displayName || cluster.name;
}

/**
 * Authorization-scoped cluster selection without eager estate downloads.
 * Results are debounced, remotely paged, and virtualized; the selected value
 * is resolved independently so it remains named even when it is not on the
 * current search page.
 */
export function RemoteClusterPicker({
  value,
  onChange,
  onBlur,
  id,
  name,
  ariaLabel = "Cluster",
  placeholder = "Select a cluster…",
  allowedClusterIds,
  excludedClusterIds,
  disabled = false,
  className,
}: RemoteClusterPickerProps) {
  const [open, setOpen] = useState(false);
  const [term, setTerm] = useState("");
  const [activeIndex, setActiveIndex] = useState(-1);
  const [debouncedTerm] = useDebouncedValue(term, { wait: 250 });
  const selectedQuery = useCluster(value);
  const searchQuery = useClusterSearch(debouncedTerm, open && !disabled);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  const allowed = useMemo(
    () => (allowedClusterIds ? new Set(allowedClusterIds) : null),
    [allowedClusterIds],
  );
  const excluded = useMemo(
    () => new Set(excludedClusterIds ?? []),
    [excludedClusterIds],
  );
  const clusters = useMemo(() => {
    const seen = new Set<string>();
    return (searchQuery.isError ? [] : (searchQuery.data?.pages ?? []))
      .flatMap((page) => page.data)
      .filter((cluster) => {
        if (seen.has(cluster.id)) return false;
        seen.add(cluster.id);
        return (
          (!allowed || allowed.has(cluster.id)) && !excluded.has(cluster.id)
        );
      });
  }, [allowed, excluded, searchQuery.data?.pages, searchQuery.isError]);

  const rowCount = clusters.length + (searchQuery.hasNextPage ? 1 : 0);
  // TanStack Virtual intentionally owns mutable measurement callbacks, so the
  // React Compiler must leave this hook boundary untouched.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 48,
    overscan: 6,
  });

  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus();
    const dismiss = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false);
        onBlur?.();
      }
    };
    document.addEventListener("mousedown", dismiss);
    return () => document.removeEventListener("mousedown", dismiss);
  }, [onBlur, open]);

  useEffect(() => {
    setActiveIndex(clusters.length > 0 ? 0 : -1);
  }, [debouncedTerm, clusters.length]);

  const select = (cluster: Cluster) => {
    onChange(cluster.id);
    setOpen(false);
    setTerm("");
    onBlur?.();
    requestAnimationFrame(() => triggerRef.current?.focus());
  };
  const selected = selectedQuery.isError ? undefined : selectedQuery.data;
  const selectedText = selected ? clusterLabel(selected) : value;
  const listboxID = `${id ?? "remote-cluster"}-listbox`;

  return (
    <div ref={rootRef} className={cn("relative", className)}>
      <button
        ref={triggerRef}
        id={id}
        name={name}
        type="button"
        role="combobox"
        aria-label={ariaLabel}
        aria-controls={listboxID}
        aria-expanded={open}
        aria-haspopup="listbox"
        disabled={disabled}
        onClick={() => setOpen((current) => !current)}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "Enter") {
            event.preventDefault();
            setOpen(true);
          }
        }}
        className="flex h-9 w-full items-center justify-between gap-2 rounded-md border border-border bg-background px-3 text-left text-sm text-foreground outline-hidden focus:ring-1 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
      >
        <span className={cn("truncate", !value && "text-muted-foreground")}>
          {value ? selectedText : placeholder}
        </span>
        <ChevronsUpDown className="h-4 w-4 shrink-0 text-muted-foreground" />
      </button>

      {open ? (
        <div className="absolute z-50 mt-1 w-full min-w-72 overflow-hidden rounded-md border border-border bg-popover shadow-xl">
          <div className="flex items-center border-b border-border px-3">
            <Search className="h-4 w-4 shrink-0 text-muted-foreground" />
            <input
              ref={inputRef}
              value={term}
              onChange={(event) => setTerm(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Escape") {
                  event.preventDefault();
                  setOpen(false);
                  onBlur?.();
                  requestAnimationFrame(() => triggerRef.current?.focus());
                  return;
                }
                if (event.key === "ArrowDown") {
                  event.preventDefault();
                  if (clusters.length === 0) return;
                  const next = Math.min(clusters.length - 1, activeIndex + 1);
                  setActiveIndex(next);
                  virtualizer.scrollToIndex(next);
                } else if (event.key === "ArrowUp") {
                  event.preventDefault();
                  const next = Math.max(0, activeIndex - 1);
                  setActiveIndex(next);
                  virtualizer.scrollToIndex(next);
                } else if (event.key === "Enter" && activeIndex >= 0) {
                  event.preventDefault();
                  const cluster = clusters[activeIndex];
                  if (cluster) select(cluster);
                }
              }}
              role="searchbox"
              aria-label="Search clusters"
              aria-controls={listboxID}
              aria-activedescendant={
                activeIndex >= 0
                  ? `${listboxID}-option-${activeIndex}`
                  : undefined
              }
              placeholder="Search by cluster name…"
              className="h-10 min-w-0 flex-1 bg-transparent px-2 text-sm outline-hidden placeholder:text-muted-foreground"
            />
          </div>

          <div
            ref={scrollRef}
            id={listboxID}
            role="listbox"
            aria-label="Cluster search results"
            aria-busy={searchQuery.isLoading || searchQuery.isFetchingNextPage}
            className="max-h-72 overflow-y-auto"
          >
            {searchQuery.isLoading || term.trim() !== debouncedTerm.trim() ? (
              <div
                role="status"
                className="flex items-center gap-2 px-3 py-4 text-sm text-muted-foreground"
              >
                <Loader2 className="h-4 w-4 animate-spin" /> Searching clusters…
              </div>
            ) : searchQuery.isError ? (
              <div role="alert" className="px-3 py-4 text-sm text-status-error">
                Could not load clusters.
                <button
                  type="button"
                  className="ml-2 underline"
                  onClick={() => void searchQuery.refetch()}
                >
                  Retry
                </button>
              </div>
            ) : rowCount === 0 ? (
              <div className="px-3 py-6 text-center text-sm text-muted-foreground">
                No clusters found.
              </div>
            ) : (
              <div
                className="relative w-full"
                style={{ height: `${virtualizer.getTotalSize()}px` }}
              >
                {virtualizer.getVirtualItems().map((virtualRow) => {
                  const cluster = clusters[virtualRow.index];
                  const loadMore = !cluster;
                  return (
                    <div
                      key={loadMore ? "load-more" : cluster.id}
                      className="absolute left-0 top-0 w-full px-1 py-0.5"
                      style={{
                        height: `${virtualRow.size}px`,
                        transform: `translateY(${virtualRow.start}px)`,
                      }}
                    >
                      {loadMore ? (
                        <button
                          type="button"
                          disabled={searchQuery.isFetchingNextPage}
                          onClick={() => void searchQuery.fetchNextPage()}
                          className="flex h-full w-full items-center justify-center rounded-sm text-sm text-primary hover:bg-accent disabled:opacity-50"
                        >
                          {searchQuery.isFetchingNextPage
                            ? "Loading more…"
                            : "Load more clusters"}
                        </button>
                      ) : (
                        <button
                          id={`${listboxID}-option-${virtualRow.index}`}
                          type="button"
                          role="option"
                          aria-selected={cluster.id === value}
                          onMouseEnter={() => setActiveIndex(virtualRow.index)}
                          onClick={() => select(cluster)}
                          className={cn(
                            "flex h-full w-full items-center gap-2 rounded-sm px-2 text-left hover:bg-accent",
                            activeIndex === virtualRow.index && "bg-accent",
                          )}
                        >
                          <span className="min-w-0 flex-1">
                            <span className="block truncate text-sm text-foreground">
                              {clusterLabel(cluster)}
                            </span>
                            <span className="block truncate text-xs text-muted-foreground">
                              {[
                                cluster.name,
                                cluster.environment,
                                cluster.region,
                              ]
                                .filter(Boolean)
                                .join(" · ")}
                            </span>
                          </span>
                          {cluster.id === value ? (
                            <Check className="h-4 w-4 shrink-0 text-primary" />
                          ) : null}
                        </button>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      ) : null}
    </div>
  );
}
