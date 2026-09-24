import { resolveClusterTransition } from "./cluster-navigation-transition";
import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useQueries } from "@tanstack/react-query";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { Command } from "cmdk";
import { ChevronsUpDown, Search, Server, Star } from "lucide-react";

import {
  ClusterOption,
  useDismissable,
} from "@/components/layout/cluster-scope-controls";
import { useClusterSearch } from "@/lib/hooks/cluster-search";
import { getCluster } from "@/lib/api/clusters";
import { queryKeys } from "@/lib/query-keys";
import {
  useClusterScopeStore,
  withClusterScopeSelection,
} from "@/lib/cluster-scope";
import { useUserPreferences } from "@/lib/user-preferences";
import { cn } from "@/lib/utils";
import type { Cluster } from "@/types";

const MAX_PINNED_CLUSTERS = 20;

const badgeDotClass: Record<string, string> = {
  slate: "bg-muted-foreground",
  blue: "bg-status-info",
  green: "bg-status-success",
  amber: "bg-status-warning",
  red: "bg-status-error",
  purple: "bg-primary",
};

const GROUP_HEADING_CLASS =
  "[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-2xs [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:text-muted-foreground";

/** One selectable row, shared by the Pinned/Recent/All shelves. */
function ClusterRow({
  cluster,
  pinned,
  onSelect,
  onTogglePin,
}: {
  cluster: Cluster;
  pinned: boolean;
  onSelect: (cluster: Cluster) => void;
  onTogglePin: (id: string) => void;
}) {
  return (
    <Command.Item
      key={cluster.id}
      value={`${cluster.displayName} ${cluster.name} ${cluster.environment} ${cluster.region}`}
      onSelect={() => onSelect(cluster)}
      className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 data-[selected=true]:bg-accent"
    >
      <ClusterOption cluster={cluster} />
      <button
        type="button"
        aria-pressed={pinned}
        aria-label={
          pinned
            ? `Unpin ${cluster.displayName || cluster.name}`
            : `Pin ${cluster.displayName || cluster.name}`
        }
        onClick={(event) => {
          event.stopPropagation();
          onTogglePin(cluster.id);
        }}
        className="shrink-0 rounded-sm p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        <Star
          className={cn("h-3.5 w-3.5", pinned && "fill-current text-primary")}
        />
      </button>
    </Command.Item>
  );
}

/** The topbar chip that opens the switcher popover. */
function ClusterSwitcherTrigger({
  triggerRef,
  open,
  onToggle,
  clusterId,
  clusterName,
  currentCluster,
  currentDotClass,
}: {
  triggerRef: RefObject<HTMLButtonElement | null>;
  open: boolean;
  onToggle: () => void;
  clusterId?: string;
  clusterName?: string;
  currentCluster?: Cluster;
  currentDotClass?: string;
}) {
  const label = clusterId
    ? currentCluster?.displayName ||
      currentCluster?.name ||
      clusterName ||
      "Cluster"
    : "Clusters";

  return (
    <button
      ref={triggerRef}
      type="button"
      onClick={onToggle}
      aria-haspopup="listbox"
      aria-expanded={open}
      aria-label={label}
      title="Switch cluster (Ctrl/Cmd+J)"
      className="flex h-8 min-w-0 shrink-0 items-center gap-1.5 rounded-md border border-border px-2 text-sm text-foreground hover:bg-accent sm:max-w-52"
    >
      <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      {currentDotClass && (
        <span
          aria-hidden="true"
          className={cn("h-2 w-2 shrink-0 rounded-full", currentDotClass)}
        />
      )}
      {/* Text collapses below `sm` so the always-mounted chip doesn't crowd
          out breadcrumbs on narrow viewports; `aria-label` above keeps the
          button's accessible name stable either way. */}
      <span className="min-w-0 max-w-32 flex-1 truncate text-left">
        {label}
      </span>
      <ChevronsUpDown className="hidden h-3.5 w-3.5 shrink-0 text-muted-foreground sm:block" />
    </button>
  );
}

/**
 * Always-mounted cluster switcher for the topbar. Unlike the deprecated
 * `SearchableClusterSwitcher` (sidebar-only, cluster-context-only), this
 * renders on every page and adds Pinned/Recent shelves ahead of search
 * results so "which cluster, take me to another one" never requires
 * navigating back to the cluster list first.
 */
export function ClusterSwitcherMenu({
  clusterId,
  clusterName,
}: {
  /** The cluster id from the current route, if any. */
  clusterId?: string;
  /** Display name for the current route's cluster, if any. */
  clusterName?: string;
}) {
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const scopes = useClusterScopeStore((state) => state.namespacesByCluster);
  const projectScopes = useClusterScopeStore((state) => state.projectByCluster);
  const recentClusterIds = useClusterScopeStore(
    (state) => state.recentClusterIds,
  );
  const { preferences, updatePreferences } = useUserPreferences();
  const pinnedIds = useMemo(
    () => preferences.pinned_clusters ?? [],
    [preferences.pinned_clusters],
  );

  const [open, setOpen] = useState(false);
  const [term, setTerm] = useState("");
  const [debouncedTerm] = useDebouncedValue(term, { wait: 250 });
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const close = () => setOpen(false);
  const restoreFocus = () => triggerRef.current?.focus();
  const ref = useDismissable(open, close, restoreFocus);

  useEffect(() => {
    if (open) searchRef.current?.focus();
  }, [open]);

  // Ctrl/Cmd+J opens the switcher. Cmd/Ctrl+K stays reserved for the global
  // command palette (command-palette.tsx) so the two never collide.
  useEffect(() => {
    function onKeydown(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "j") {
        event.preventDefault();
        setOpen((value) => !value);
      }
    }
    document.addEventListener("keydown", onKeydown);
    return () => document.removeEventListener("keydown", onKeydown);
  }, []);

  const searchQuery = useClusterSearch(debouncedTerm, open);
  const searchResults = useMemo(
    () => searchQuery.data?.pages.flatMap((page) => page.data) ?? [],
    [searchQuery.data],
  );

  // Fetch cluster details for pinned + recent ids that the search results
  // (which only cover the current page/term) may not already include.
  const detailIds = useMemo(() => {
    const seen = new Set<string>();
    const ids: string[] = [];
    for (const id of [...pinnedIds, ...recentClusterIds]) {
      if (!seen.has(id)) {
        seen.add(id);
        ids.push(id);
      }
    }
    return ids;
  }, [pinnedIds, recentClusterIds]);

  // useQueries' return array isn't referentially stable, so destructure the
  // piece we need (each query's data) before it flows anywhere memoized.
  const detailData = useQueries({
    queries: detailIds.map((id) => ({
      queryKey: queryKeys.clusters.detail(id),
      queryFn: ({ signal }: { signal?: AbortSignal }) => getCluster(id, signal),
      enabled: open,
      staleTime: 30_000,
    })),
  }).map((query) => query.data);

  const detailById = useMemo(() => {
    const map = new Map<string, Cluster>();
    detailIds.forEach((id, index) => {
      const data = detailData[index];
      if (data) map.set(id, data);
    });
    for (const cluster of searchResults) map.set(cluster.id, cluster);
    return map;
  }, [detailIds, detailData, searchResults]);

  const pinnedClusters = pinnedIds
    .map((id) => detailById.get(id))
    .filter((cluster): cluster is Cluster => !!cluster);
  const recentClusters = recentClusterIds
    .filter((id) => !pinnedIds.includes(id))
    .map((id) => detailById.get(id))
    .filter((cluster): cluster is Cluster => !!cluster);
  const resultClusters = searchResults.filter(
    (cluster) =>
      !pinnedIds.includes(cluster.id) && !recentClusterIds.includes(cluster.id),
  );

  const isPinned = (id: string) => pinnedIds.includes(id);
  const togglePin = (id: string) => {
    const next = isPinned(id)
      ? pinnedIds.filter((pinnedId) => pinnedId !== id)
      : [...pinnedIds, id].slice(0, MAX_PINNED_CLUSTERS);
    updatePreferences({ pinned_clusters: next });
  };

  const transitionRequest = useRef(0);
  const [transitionState, setTransitionState] = useState("");
  useEffect(
    () => () => {
      transitionRequest.current++;
    },
    [pathname],
  );
  const select = async (next: Cluster) => {
    const request = ++transitionRequest.current;
    setTransitionState("Resolving target cluster scope…");
    try {
      const target = await resolveClusterTransition(
        pathname,
        next.id,
        scopes[next.id] ?? null,
        projectScopes[next.id] ?? null,
      );
      if (request !== transitionRequest.current) return;
      void navigate({
        to: withClusterScopeSelection(
          target.path,
          new URLSearchParams(),
          target.namespaces,
          target.projectId,
        ),
      });
      setTransitionState("");
      close();
      requestAnimationFrame(restoreFocus);
    } catch {
      if (request === transitionRequest.current)
        setTransitionState(
          "Target scope could not be resolved. Select the cluster again to retry; your current scope is unchanged.",
        );
    }
  };

  const currentCluster = clusterId ? detailById.get(clusterId) : undefined;
  const currentDotClass = currentCluster?.badgeColor
    ? (badgeDotClass[currentCluster.badgeColor] ?? undefined)
    : undefined;

  const renderRow = (cluster: Cluster) => (
    <ClusterRow
      key={cluster.id}
      cluster={cluster}
      pinned={isPinned(cluster.id)}
      onSelect={select}
      onTogglePin={togglePin}
    />
  );

  return (
    <div ref={ref} className="relative min-w-0 shrink-0">
      <ClusterSwitcherTrigger
        triggerRef={triggerRef}
        open={open}
        onToggle={() => setOpen((value) => !value)}
        clusterId={clusterId}
        clusterName={clusterName}
        currentCluster={currentCluster}
        currentDotClass={currentDotClass}
      />
      {open ? (
        <Command
          shouldFilter={false}
          className="absolute left-0 top-full z-50 mt-1 w-80 max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-border bg-popover shadow-xl"
        >
          <div className="flex items-center border-b border-border px-3">
            <Search className="h-4 w-4 text-muted-foreground" />
            <Command.Input
              ref={searchRef}
              value={term}
              onValueChange={setTerm}
              placeholder="Find a cluster..."
              className="h-10 min-w-0 flex-1 bg-transparent px-2 text-sm outline-hidden placeholder:text-muted-foreground"
            />
          </div>
          <Command.List className="max-h-96 overflow-y-auto p-1" role="listbox">
            {transitionState && (
              <p role="status" className="p-3 text-sm">
                {transitionState}
              </p>
            )}
            {pinnedClusters.length > 0 && (
              <Command.Group heading="Pinned" className={GROUP_HEADING_CLASS}>
                {pinnedClusters.map(renderRow)}
              </Command.Group>
            )}
            {recentClusters.length > 0 && (
              <Command.Group heading="Recent" className={GROUP_HEADING_CLASS}>
                {recentClusters.map(renderRow)}
              </Command.Group>
            )}
            {searchQuery.isLoading || term.trim() !== debouncedTerm.trim() ? (
              <Command.Loading className="px-3 py-4 text-sm text-muted-foreground">
                Searching clusters...
              </Command.Loading>
            ) : searchQuery.isError ? (
              <div role="alert" className="px-3 py-4 text-sm">
                Could not load clusters.
                <button
                  type="button"
                  onClick={() => void searchQuery.refetch()}
                  className="ml-2 underline"
                >
                  Retry
                </button>
              </div>
            ) : resultClusters.length === 0 &&
              pinnedClusters.length === 0 &&
              recentClusters.length === 0 ? (
              <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">
                No clusters found.
              </Command.Empty>
            ) : null}
            {resultClusters.length > 0 && (
              <Command.Group
                heading={
                  pinnedClusters.length > 0 || recentClusters.length > 0
                    ? "All clusters"
                    : undefined
                }
                className={GROUP_HEADING_CLASS}
              >
                {resultClusters.map(renderRow)}
              </Command.Group>
            )}
            {searchQuery.hasNextPage ? (
              <Command.Item
                value="load-more-clusters"
                disabled={searchQuery.isFetchingNextPage}
                onSelect={() => void searchQuery.fetchNextPage()}
                className="cursor-pointer rounded-md px-3 py-2 text-center text-sm text-primary data-[selected=true]:bg-accent"
              >
                {searchQuery.isFetchingNextPage
                  ? "Loading..."
                  : "Load more clusters"}
              </Command.Item>
            ) : null}
          </Command.List>
        </Command>
      ) : null}
    </div>
  );
}
