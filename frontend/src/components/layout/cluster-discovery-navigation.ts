import { Puzzle, Star } from "lucide-react";
import type { ClusterDiscovery } from "./cluster-discovery-model";
import type { NavGroup, NavItem } from "./sidebar-navigation";
import { filterNavigationItems, navGroupItems } from "./nav-group-items";
import { crdGroupLabel } from "@/lib/crd-group-labels";

export function filterNavGroupsByDiscovery(
  groups: NavGroup[],
  discovery: ClusterDiscovery,
): NavGroup[] {
  if (discovery.isLoading || discovery.isError) return groups;
  return filterNavigationItems(
    groups,
    (item) =>
      (!item.ifHaveGroup || discovery.groups.has(item.ifHaveGroup)) &&
      (!item.ifHaveKind || discovery.kinds.has(item.ifHaveKind)),
  );
}

export function withDiscoveredNavigation(
  groups: NavGroup[],
  discovery: ClusterDiscovery,
  clusterId: string,
  starred: readonly string[] = [],
  limit = 40,
): NavGroup[] {
  const represented = new Set(
    groups.flatMap(navGroupItems).map((item) => item.resourceType),
  );
  const base = `/dashboard/clusters/${clusterId}/custom-resources`;
  const reserved = new Set(
    [...discovery.crdsByGroup.values()]
      .flat()
      .map((resource) => `${resource.group}/${resource.plural}`)
      .filter((type) => starred.includes(type) && !represented.has(type))
      .slice(0, 20),
  );
  let remaining = limit - reserved.size;
  let truncated = false;
  const subgroups = [...discovery.crdsByGroup.entries()]
    .sort(
      ([left], [right]) =>
        crdGroupLabel(left).localeCompare(crdGroupLabel(right)) ||
        left.localeCompare(right),
    )
    .map(([group, resources]) => {
      const items: NavItem[] = [];
      for (const resource of [...resources].sort((a, b) =>
        a.kind.localeCompare(b.kind),
      )) {
        const resourceType = `${group}/${resource.plural}`;
        if (represented.has(resourceType)) continue;
        if (remaining === 0 && !reserved.has(resourceType)) {
          truncated = true;
          continue;
        }
        if (!reserved.has(resourceType)) remaining--;
        items.push({
          label: resource.kind,
          icon: Puzzle,
          resourceType,
          href: `${base}/${group}/${resource.version}/${resource.plural}`,
          countKey: `crd:${resourceType}`,
          permission: { resource: "custom_resources", verb: "list" },
          projectPermission: resource.namespaced
            ? { resource: "custom_resources", verb: "list" }
            : undefined,
        });
      }
      return { label: crdGroupLabel(group), items };
    })
    .filter((subgroup) => subgroup.items.length > 0);
  return groups.map((group) =>
    group.label === "More Resources"
      ? {
          ...group,
          subgroups: [
            ...(group.subgroups ?? []),
            ...subgroups,
            ...(truncated
              ? [
                  {
                    label: "Browse",
                    items: [
                      {
                        label: "All custom resources →",
                        href: base,
                        icon: Puzzle,
                        permission: {
                          resource: "custom_resources",
                          verb: "read" as const,
                        },
                      },
                    ],
                  },
                ]
              : []),
          ],
        }
      : group,
  );
}

export function withStarredTypes(
  groups: NavGroup[],
  starred: readonly string[],
): NavGroup[] {
  const byType = new Map(
    groups
      .flatMap(navGroupItems)
      .filter((item) => item.resourceType)
      .map((item) => [item.resourceType, item]),
  );
  const items = [...new Set(starred)].flatMap((type) => {
    const item = byType.get(type);
    return item ? [item] : [];
  });
  return items.length
    ? [{ label: "Starred", icon: Star, defaultOpen: true, items }, ...groups]
    : groups;
}
