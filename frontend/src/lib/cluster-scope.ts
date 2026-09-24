import { useCallback, useEffect, useMemo } from "react";

import { useClusterNamespaces } from "@/lib/hooks/clusters";
import { useMyEffectivePermissions } from "@/lib/hooks/rbac";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { createBrowserState } from "@/lib/browser-state";

export const NAMESPACE_SCOPE_PARAM = "namespaces";
export const PROJECT_SCOPE_PARAM = "project";

/** `null` means every authorized namespace; an array is an explicit allow-list. */
export type NamespaceSelection = readonly string[] | null;

export const MAX_RECENT_CLUSTERS = 5;

/** Push `clusterId` to the front of `recent`, de-duped, capped at the limit. */
export function withRecentCluster(
  recent: readonly string[],
  clusterId: string,
): string[] {
  return [clusterId, ...recent.filter((id) => id !== clusterId)].slice(
    0,
    MAX_RECENT_CLUSTERS,
  );
}

interface ClusterScopeState extends Record<string, unknown> {
  lastClusterId: string | null;
  namespacesByCluster: Record<string, NamespaceSelection>;
  projectByCluster: Record<string, string | null>;
  /** Most-recently-visited clusters, most-recent-first, capped at MAX_RECENT_CLUSTERS. */
  recentClusterIds: string[];
  setClusterScope: (
    clusterId: string,
    namespaces: NamespaceSelection,
    projectId: string | null,
  ) => void;
}

export const useClusterScopeStore = createBrowserState<ClusterScopeState>(
  {
    lastClusterId: null,
    namespacesByCluster: {},
    projectByCluster: {},
    recentClusterIds: [],
    setClusterScope: (clusterId, namespaces, projectId) =>
      useClusterScopeStore.setState((state) => ({
        lastClusterId: clusterId,
        namespacesByCluster: {
          ...state.namespacesByCluster,
          [clusterId]:
            namespaces === null ? null : canonicalNamespaces(namespaces),
        },
        projectByCluster: {
          ...state.projectByCluster,
          [clusterId]: projectId,
        },
        recentClusterIds: withRecentCluster(state.recentClusterIds, clusterId),
      })),
  },
  {
    storageKey: "astronomer-cluster-scope",
    version: 2,
    persist: (state) => ({
      lastClusterId: state.lastClusterId,
      namespacesByCluster: state.namespacesByCluster,
      projectByCluster: state.projectByCluster,
      recentClusterIds: state.recentClusterIds,
    }),
  },
);

export function canonicalNamespaces(namespaces: readonly string[]): string[] {
  return [
    ...new Set(namespaces.map((value) => value.trim()).filter(Boolean)),
  ].sort((left, right) => left.localeCompare(right));
}

export function parseNamespaceSelection(
  search: URLSearchParams,
): readonly string[] | undefined {
  if (!search.has(NAMESPACE_SCOPE_PARAM)) return undefined;
  const raw = search.get(NAMESPACE_SCOPE_PARAM) ?? "";
  return canonicalNamespaces(raw.split(","));
}

function parseProjectSelection(search: URLSearchParams): string | undefined {
  if (!search.has(PROJECT_SCOPE_PARAM)) return undefined;
  return search.get(PROJECT_SCOPE_PARAM)?.trim() || undefined;
}

export function withClusterScopeSelection(
  pathname: string,
  search: URLSearchParams,
  namespaces: NamespaceSelection,
  projectId: string | null,
): string {
  const next = new URLSearchParams(search);
  for (const key of [
    "page",
    "selected",
    "continue",
    "version_page",
    "cluster_page",
  ])
    next.delete(key);
  if (namespaces === null) next.delete(NAMESPACE_SCOPE_PARAM);
  else
    next.set(NAMESPACE_SCOPE_PARAM, canonicalNamespaces(namespaces).join(","));
  if (projectId) next.set(PROJECT_SCOPE_PARAM, projectId);
  else next.delete(PROJECT_SCOPE_PARAM);
  const query = next.toString();
  return query ? `${pathname}?${query}` : pathname;
}

function ruleGrantsNamespaceVisibility(rule: {
  resource: string;
  verbs?: string[];
}): boolean {
  const resources = new Set([
    "*",
    "clusters",
    "workloads",
    "pods",
    "services",
    "ingresses",
    "network_policies",
    "storage",
    "configmaps",
    "secrets",
  ]);
  return (
    resources.has(rule.resource) &&
    Boolean(
      rule.verbs?.some(
        (verb) => verb === "*" || verb === "read" || verb === "list",
      ),
    )
  );
}

function sourceApplies(
  source: {
    scope: string;
    clusterId?: string;
    namespace?: string;
  },
  clusterId: string,
  namespace: string | undefined,
  authorizedNamespaces: readonly string[],
): boolean {
  if (source.scope === "global")
    return !source.namespace || source.namespace === namespace;
  if (source.scope === "cluster") {
    return (
      source.clusterId === clusterId &&
      (!source.namespace || source.namespace === namespace)
    );
  }
  // The namespaces endpoint is authorization-filtered. A project binding may
  // therefore apply only to namespace rows the server returned for this cluster.
  if (source.scope === "project" && namespace) {
    return (
      authorizedNamespaces.includes(namespace) &&
      (!source.namespace || source.namespace === namespace)
    );
  }
  return false;
}

export interface ClusterNamespaceScope {
  availableNamespaces: readonly string[];
  selectedNamespaces: NamespaceSelection | undefined;
  restricted: boolean;
  ready: boolean;
  selectedProjectId: string | null;
  setSelectedNamespaces: (selection: NamespaceSelection) => void;
  setProjectScope: (
    projectId: string | null,
    namespaces: NamespaceSelection,
  ) => void;
  includes: (namespace: string | undefined) => boolean;
  allows: (resource: string, verb: string, namespace?: string) => boolean;
}

/**
 * One route-aware namespace scope for cluster navigation and explorer tables.
 * The URL is authoritative, local storage restores the last scope, and a
 * namespace-confined caller never transiently falls back to an unscoped view.
 */
export function useClusterNamespaceScope(
  clusterId: string,
): ClusterNamespaceScope {
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const searchString = search.toString();
  const stored = useClusterScopeStore(
    (state) => state.namespacesByCluster[clusterId],
  );
  const storedProjectId = useClusterScopeStore(
    (state) => state.projectByCluster[clusterId],
  );
  const setStored = useClusterScopeStore((state) => state.setClusterScope);
  const namespacesQuery = useClusterNamespaces(clusterId);
  const permissionsQuery = useMyEffectivePermissions({ clusterId });
  const availableNamespaces = useMemo(
    () =>
      canonicalNamespaces(
        (namespacesQuery.data ?? []).map((namespace) => namespace.name),
      ),
    [namespacesQuery.data],
  );

  const broadNamespaceAccess = useMemo(() => {
    const response = permissionsQuery.data;
    if (!response) return false;
    if (response.superuser) return true;
    return response.bindings.some((binding) => {
      if (binding.namespace) return false;
      if (binding.scope === "cluster" && binding.clusterId !== clusterId)
        return false;
      if (binding.scope !== "cluster" && binding.scope !== "global")
        return false;
      return binding.rules?.some(ruleGrantsNamespaceVisibility);
    });
  }, [clusterId, permissionsQuery.data]);
  const restricted = !broadNamespaceAccess;
  const ready = namespacesQuery.isSuccess && permissionsQuery.isSuccess;
  const urlSelection = useMemo(
    () => parseNamespaceSelection(new URLSearchParams(searchString)),
    [searchString],
  );
  const urlProjectId = useMemo(
    () => parseProjectSelection(new URLSearchParams(searchString)),
    [searchString],
  );
  const selectedProjectId = urlProjectId ?? storedProjectId ?? null;
  const activeSelection = useMemo<NamespaceSelection | undefined>(() => {
    if (!ready) return undefined;
    if (urlSelection !== undefined) {
      return canonicalNamespaces(
        urlSelection.filter((namespace) =>
          availableNamespaces.includes(namespace),
        ),
      );
    }
    // Never render an unrestricted frame while a confined user's persisted
    // state is absent or stale. The initialization effect fills the exact
    // server-authorized namespace set and writes it into the URL.
    if (restricted && (stored === undefined || stored === null)) return [];
    return stored ?? null;
  }, [availableNamespaces, ready, restricted, stored, urlSelection]);

  const commit = useCallback(
    (selection: NamespaceSelection, projectId: string | null) => {
      const allowed = new Set(availableNamespaces);
      const safe =
        selection === null && !restricted
          ? null
          : canonicalNamespaces(
              (selection ?? availableNamespaces).filter((namespace) =>
                allowed.has(namespace),
              ),
            );
      setStored(clusterId, safe, projectId);
      void navigate({
        to: withClusterScopeSelection(
          pathname,
          new URLSearchParams(searchString),
          safe,
          projectId,
        ),
        replace: true,
      });
    },
    [
      availableNamespaces,
      clusterId,
      pathname,
      restricted,
      navigate,
      searchString,
      setStored,
    ],
  );
  const setSelectedNamespaces = useCallback(
    (selection: NamespaceSelection) => commit(selection, null),
    [commit],
  );
  const setProjectScope = useCallback(
    (projectId: string | null, namespaces: NamespaceSelection) =>
      commit(namespaces, projectId),
    [commit],
  );

  useEffect(() => {
    if (!ready) return;
    const fromUrl = urlSelection;
    if (fromUrl !== undefined) {
      const safe = canonicalNamespaces(
        fromUrl.filter((namespace) => availableNamespaces.includes(namespace)),
      );
      if (JSON.stringify(safe) !== JSON.stringify(fromUrl)) {
        commit(safe, selectedProjectId);
      } else if (JSON.stringify(safe) !== JSON.stringify(stored)) {
        setStored(clusterId, safe, selectedProjectId);
      }
      return;
    }
    if (stored !== undefined && !(restricted && stored === null)) {
      if (stored !== null) commit(stored, selectedProjectId);
      return;
    }
    commit(
      restricted ? availableNamespaces : (stored ?? null),
      selectedProjectId,
    );
  }, [
    availableNamespaces,
    clusterId,
    commit,
    ready,
    restricted,
    searchString,
    setStored,
    stored,
    selectedProjectId,
    urlSelection,
  ]);

  const includes = useCallback(
    (namespace: string | undefined) => {
      if (!namespace) return true;
      if (activeSelection === undefined) return false;
      return activeSelection === null || activeSelection.includes(namespace);
    },
    [activeSelection],
  );

  const allows = useCallback(
    (resource: string, verb: string, namespace?: string) => {
      const response = permissionsQuery.data;
      if (!ready || !response || !includes(namespace)) return false;
      if (response.superuser) return true;
      return response.permissions.some(
        (grant) =>
          (grant.resource === "*" || grant.resource === resource) &&
          (grant.verb === "*" || grant.verb === verb) &&
          grant.sources.some((source) =>
            sourceApplies(source, clusterId, namespace, availableNamespaces),
          ),
      );
    },
    [availableNamespaces, clusterId, includes, permissionsQuery.data, ready],
  );

  return {
    availableNamespaces,
    selectedNamespaces: activeSelection,
    restricted,
    ready,
    selectedProjectId,
    setSelectedNamespaces,
    setProjectScope,
    includes,
    allows,
  };
}
