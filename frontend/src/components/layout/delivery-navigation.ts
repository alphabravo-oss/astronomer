import {
  Rocket,
  SlidersHorizontal,
  SlidersVertical,
  GitBranch,
  Package,
  Target,
  Workflow,
  Layers,
} from "lucide-react";
import type { NavItem } from "./sidebar-navigation";

/**
 * Delivery pillar. "Fleet" replaces the old "Estate"; Flux-level deployment
 * records are "Releases" so the Kubernetes Deployment kind (cluster panel ›
 * Workloads) stays unambiguous. "Rollouts" remains the progressive
 * placement-attempt noun it already was.
 */
export const DELIVERY_DESTINATIONS: NavItem[] = [
  {
    label: "Fleet",
    href: "/dashboard/delivery",
    icon: Rocket,
    exact: true,
    permission: { resource: "delivery_inventory", verb: "read" },
    projectPermission: { resource: "delivery_targets", verb: "list" },
  },
  ...(
    [
      ["sources", "Sources", GitBranch],
      ["bundles", "Bundles", Package],
      ["targets", "Targets", Target],
      ["rollouts", "Rollouts", Workflow],
      ["deployments", "Releases", Layers],
    ] as const
  ).map(([name, label, icon]) => ({
    label,
    href: `/dashboard/delivery/${name}`,
    icon,
    permission: { resource: `delivery_${name}`, verb: "list" as const },
    projectPermission: {
      resource: `delivery_${name}`,
      verb: "list" as const,
    },
  })),
  {
    label: "Templates",
    href: "/dashboard/delivery/configuration-templates",
    icon: SlidersHorizontal,
    permission: { resource: "delivery_configuration_templates", verb: "list" },
    projectPermission: {
      resource: "delivery_configuration_templates",
      verb: "list",
    },
  },
  {
    label: "Overrides",
    href: "/dashboard/delivery/override-sets",
    icon: SlidersVertical,
    permission: { resource: "delivery_configuration_templates", verb: "list" },
    projectPermission: {
      resource: "delivery_configuration_templates",
      verb: "list",
    },
  },
];
