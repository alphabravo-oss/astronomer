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

export const DELIVERY_DESTINATIONS: NavItem[] = [
  {
    label: "Estate",
    href: "/dashboard/delivery",
    icon: Rocket,
    exact: true,
    permission: { resource: "delivery_inventory", verb: "read" },
    projectPermission: { resource: "delivery_targets", verb: "list" },
  },
  ...["sources", "bundles", "targets", "rollouts", "deployments"].map(
    (name, index) => ({
      label: name[0].toUpperCase() + name.slice(1),
      href: `/dashboard/delivery/${name}`,
      icon: [GitBranch, Package, Target, Workflow, Layers][index],
      permission: { resource: `delivery_${name}`, verb: "list" as const },
      projectPermission: {
        resource: `delivery_${name}`,
        verb: "list" as const,
      },
    }),
  ),
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
