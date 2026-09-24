import type { NamespaceSelection } from "./cluster-scope";

/** Explicit selection is always applied before server pagination. */
export function collectionScope(selection: NamespaceSelection | undefined) {
  if (selection === undefined)
    return { enabled: false, message: "Resolving namespace scope…" };
  if (selection === null) return { enabled: true, namespace: undefined };
  if (selection.length === 1) return { enabled: true, namespace: selection[0] };
  if (selection.length > 1)
    return { enabled: true, namespaces: [...selection] };
  return {
    enabled: false,
    message: "No namespaces selected. Select a namespace to view resources.",
  };
}

export function projectInCluster(
  project: { clusterId?: string; clusterIds?: string[] },
  clusterId: string,
) {
  return (
    project.clusterId === clusterId ||
    Boolean(project.clusterIds?.includes(clusterId))
  );
}

/** Project changes replace namespace overrides and invalidate object/page state atomically. */
export function projectSelectionSearch(
  search: URLSearchParams,
  projectId: string,
  namespaces?: readonly string[],
) {
  const next = new URLSearchParams(search);
  for (const key of [
    "page",
    "version_page",
    "cluster_page",
    "selected",
    "continue",
    "install",
  ])
    next.delete(key);
  if (projectId) {
    next.set("project", projectId);
    // Unresolved selection fails closed until the selected project is read.
    next.set("namespaces", (namespaces ?? []).join(","));
  } else {
    next.delete("project");
    next.delete("namespaces");
  }
  return next;
}
