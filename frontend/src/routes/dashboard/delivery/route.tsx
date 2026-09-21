import { createFileRoute, Outlet } from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import { useLocation } from "@tanstack/react-router";
import {
  Boxes,
  Crosshair,
  GitBranch,
  Layers,
  Rocket,
  Route as RouteIcon,
  SlidersHorizontal,
  SlidersVertical,
} from "lucide-react";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabsList } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

const tabs = [
  { key: "estate", label: "Estate", icon: Rocket, segment: "" },
  {
    key: "deployments",
    label: "Deployments",
    icon: Layers,
    segment: "/deployments",
  },
  { key: "rollouts", label: "Rollouts", icon: RouteIcon, segment: "/rollouts" },
  { key: "sources", label: "Sources", icon: GitBranch, segment: "/sources" },
  { key: "bundles", label: "Bundles", icon: Boxes, segment: "/bundles" },
  { key: "targets", label: "Targets", icon: Crosshair, segment: "/targets" },
  {
    key: "configuration-templates",
    label: "Templates",
    icon: SlidersHorizontal,
    segment: "/configuration-templates",
  },
  {
    key: "override-sets",
    label: "Overrides",
    icon: SlidersVertical,
    segment: "/override-sets",
  },
] as const;

const base = "/dashboard/delivery";

function DeliveryEstateLayout() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const remaining = pathname.startsWith(base)
    ? pathname.slice(base.length)
    : "";
  const activeTab =
    tabs
      .filter(
        (tab) =>
          tab.segment &&
          (remaining === tab.segment ||
            remaining.startsWith(`${tab.segment}/`)),
      )
      .sort((a, b) => b.segment.length - a.segment.length)[0] ?? tabs[0];

  return (
    <PageShell>
      <PageHeader eyebrow="Continuous Delivery" title={activeTab.label} />
      <div className="border-b border-border">
        <TabsList className="flex-wrap gap-4">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            const href = `${base}${tab.segment}`;
            const active = activeTab.key === tab.key;
            return (
              <RouterLink
                key={tab.key}
                to={href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex items-center gap-2 border-b-2 pb-3 text-sm font-medium transition-colors",
                  active
                    ? "border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </RouterLink>
            );
          })}
        </TabsList>
      </div>
      <Outlet />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/delivery")({
  component: DeliveryEstateLayout,
});
