export const CRD_DISCOVERY_PATH =
  "apis/apiextensions.k8s.io/v1/customresourcedefinitions";

export interface DiscoveredResourceType {
  group: string;
  version: string;
  servedVersions: string[];
  plural: string;
  kind: string;
  namespaced: boolean;
}

export interface ClusterDiscovery {
  groups: Set<string>;
  kinds: Set<string>;
  crdsByGroup: Map<string, DiscoveredResourceType[]>;
  isLoading: boolean;
  isError: boolean;
  retry?: () => unknown;
}

interface CRDDefinition {
  spec?: {
    group?: string;
    scope?: string;
    names?: { plural?: string; kind?: string };
    versions?: { name?: string; served?: boolean; storage?: boolean }[];
  };
}

export function clusterDiscoveryFromDefinitions(
  definitions: CRDDefinition[],
): Omit<ClusterDiscovery, "isLoading" | "isError"> {
  const groups = new Set<string>();
  const kinds = new Set<string>();
  const crdsByGroup = new Map<string, DiscoveredResourceType[]>();
  const seen = new Set<string>();
  for (const { spec } of definitions) {
    const { group, scope, names, versions = [] } = spec ?? {};
    const servedVersions = versions.flatMap((v) =>
      v.served && v.name && /^[a-z0-9][a-z0-9.-]*$/.test(v.name)
        ? [v.name]
        : [],
    );
    const version =
      versions.find((v) => v.storage && servedVersions.includes(v.name ?? ""))
        ?.name ?? servedVersions[0];
    const plural = names?.plural;
    const kind = names?.kind;
    if (!group || !version || !plural || !kind) continue;
    if (
      ![group, version, plural].every((part) =>
        /^[a-z0-9][a-z0-9.-]*$/.test(part),
      )
    )
      continue;
    if (scope !== "Namespaced" && scope !== "Cluster") continue;
    const key = `${group}/${plural}`;
    if (seen.has(key)) continue;
    seen.add(key);
    groups.add(group);
    kinds.add(`${group}/${kind}`);
    const resources = crdsByGroup.get(group) ?? [];
    resources.push({
      group,
      version,
      servedVersions,
      plural,
      kind,
      namespaced: scope === "Namespaced",
    });
    crdsByGroup.set(group, resources);
  }
  return { groups, kinds, crdsByGroup };
}

export function clusterDiscoveryFromSummaries(
  summaries: Array<Record<string, unknown>>,
) {
  return clusterDiscoveryFromDefinitions(
    summaries.map((summary) => ({
      spec: {
        group: summary.group as string,
        scope: summary.scope as string,
        names: {
          kind: summary.kind as string,
          plural: summary.plural as string,
        },
        versions: (
          (summary.versions as Array<{ name: string; storage?: boolean }>) ?? []
        ).map((version) => ({ ...version, served: true })),
      },
    })),
  );
}
