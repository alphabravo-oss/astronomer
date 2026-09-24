import { createFileRoute, Outlet } from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import { useLocation } from "@tanstack/react-router";
import { Rocket, SlidersHorizontal, SlidersVertical } from "lucide-react";
import { PageShell } from "@/components/ui/page";
import { cn } from "@/lib/utils";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import {
  useDeliveryProjectScope,
  withProjectQuery,
} from "@/components/delivery/shared";

const tabs = [
  { key: "estate", label: "Estate", icon: Rocket, segment: "" },
  { key: "sources", label: "Sources", icon: Rocket, segment: "/sources" },
  { key: "bundles", label: "Bundles", icon: Rocket, segment: "/bundles" },
  { key: "targets", label: "Targets", icon: Rocket, segment: "/targets" },
  { key: "rollouts", label: "Rollouts", icon: Rocket, segment: "/rollouts" },
  {
    key: "deployments",
    label: "Deployments",
    icon: Rocket,
    segment: "/deployments",
  },
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
  const { projectId, setProjectId } = useDeliveryProjectScope();
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
      <div className="mb-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
        Continuous Delivery
      </div>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span>Project-scoped delivery views</span>
        <RemoteProjectPicker
          value={projectId}
          onChange={setProjectId}
          ariaLabel="Delivery project"
        />
      </div>
      <nav
        aria-label="Continuous Delivery sections"
        className="flex gap-6 overflow-x-auto border-b border-border"
      >
        {tabs.map((tab) => {
          const Icon = tab.icon;
          const href = withProjectQuery(`${base}${tab.segment}`, projectId);
          const active = activeTab.key === tab.key;
          return (
            <RouterLink
              key={tab.key}
              to={href}
              aria-current={active ? "page" : undefined}
              className={cn(
                "flex shrink-0 items-center gap-2 border-b-2 pb-3 text-sm font-medium transition-colors",
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
      </nav>
      <Outlet />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/delivery")({
  component: DeliveryEstateLayout,
});
