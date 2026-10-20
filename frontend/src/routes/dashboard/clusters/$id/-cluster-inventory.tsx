import { Link as RouterLink } from "@tanstack/react-router";
import { useClusterInventoryCounts } from "@/components/layout/use-sidebar-resource-counts";
import { useSidebarNavigation } from "@/components/layout/use-sidebar-navigation";
import { navGroupItems } from "@/components/layout/nav-group-items";
import { PageSection } from "@/ui";

/**
 * Inventory by group on the cluster overview. The counts that used to be
 * right-aligned numbers in the sidebar live here, keyed by the same
 * `countKey`s the cluster panel registry declares, so navigation and the
 * overview always agree.
 */
export function ClusterInventory({ clusterId }: { clusterId: string }) {
  const counts = useClusterInventoryCounts(clusterId) as Record<
    string,
    number | undefined
  >;
  const { navGroups } = useSidebarNavigation(clusterId);
  const groups = navGroups
    .map((group) => ({
      label: group.label,
      href: group.href,
      items: navGroupItems(group).filter(
        (item) => item.countKey && counts[item.countKey] !== undefined,
      ),
    }))
    .filter((group) => group.items.length > 0);

  if (groups.length === 0) return null;

  return (
    <PageSection title="Inventory" description="Resources in the selected namespaces">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {groups.map((group) => {
          const total = group.items.reduce(
            (sum, item) => sum + (counts[item.countKey!] ?? 0),
            0,
          );
          return (
            <div
              key={group.label}
              className="rounded-lg border border-border bg-card"
              data-testid={`inventory-group-${group.label}`}
            >
              <div className="flex items-center justify-between border-b border-border px-4 py-2">
                <h3 className="text-sm font-semibold text-foreground">
                  {group.label}
                </h3>
                <span className="text-meta tabular-nums text-muted-foreground">
                  {total.toLocaleString()}
                </span>
              </div>
              <ul className="divide-y divide-border">
                {group.items.map((item) => (
                  <li key={item.href}>
                    <RouterLink
                      to={item.href}
                      className="flex items-center justify-between px-4 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent/40 hover:text-foreground"
                    >
                      <span className="flex items-center gap-2">
                        <item.icon className="h-3.5 w-3.5" aria-hidden />
                        {item.label}
                      </span>
                      <span className="tabular-nums text-foreground">
                        {(counts[item.countKey!] ?? 0).toLocaleString()}
                      </span>
                    </RouterLink>
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
      </div>
    </PageSection>
  );
}
