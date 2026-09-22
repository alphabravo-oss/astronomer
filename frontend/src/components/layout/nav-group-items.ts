import type { NavGroup, NavItem } from "./sidebar-navigation";

export function navGroupItems(group: NavGroup): NavItem[] {
  return [
    ...group.items,
    ...(group.subgroups ?? []).flatMap((subgroup) => subgroup.items),
  ];
}

export function filterNavigationItems(
  groups: NavGroup[],
  predicate: (item: NavItem) => boolean,
): NavGroup[] {
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter(predicate),
      subgroups: group.subgroups
        ?.map((subgroup) => ({
          ...subgroup,
          items: subgroup.items.filter(predicate),
        }))
        .filter((subgroup) => subgroup.items.length > 0),
    }))
    .filter((group) => navGroupItems(group).length > 0);
}
