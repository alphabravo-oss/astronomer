import type { LucideIcon } from "lucide-react";
import {
  Activity,
  BarChart3,
  FileArchive,
  FileSearch,
  FileText,
  FolderGit2,
  FolderTree,
  Gauge,
  KeyRound,
  LayoutDashboard,
  Mail,
  Network,
  Palette,
  Puzzle,
  Radio,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  Users,
  Webhook,
} from "lucide-react";

export type SettingsNavigationItem = {
  href: string;
  title: string;
  description: string;
  icon: LucideIcon;
  charlie?: boolean;
  featureFlag?: "feature.extensions";
};

export type SettingsNavigationGroup = {
  label: string;
  description: string;
  items: readonly SettingsNavigationItem[];
};

/**
 * The single registry for settings discovery, hub cards, and persistent
 * navigation. Add a settings landing page here once; every settings surface
 * will discover it without maintaining a second menu.
 */
export const SETTINGS_NAVIGATION: readonly SettingsNavigationGroup[] = [
  {
    label: "Experience",
    description: "Shape the console, assistance, and operator defaults.",
    items: [
      {
        href: "/dashboard/settings/general",
        title: "General",
        description:
          "Platform identity, audit defaults, API tokens, and support.",
        icon: Settings,
      },
      {
        href: "/dashboard/settings/platform",
        title: "Platform",
        description:
          "Branding, banners, feature flags, sessions, and telemetry.",
        icon: Palette,
      },
      {
        href: "/dashboard/settings/widgets",
        title: "Dashboard widgets",
        description:
          "Prometheus, Grafana, and URL widgets for operator dashboards.",
        icon: LayoutDashboard,
      },
      {
        href: "/dashboard/settings/charlie",
        title: "Charlie",
        description:
          "AI access, automation policy, diagnostics, and product-agent controls.",
        icon: Sparkles,
        charlie: true,
      },
    ],
  },
  {
    label: "Identity & access",
    description: "Control sign-in, groups, and administrative authority.",
    items: [
      {
        href: "/dashboard/settings/auth",
        title: "Authentication",
        description:
          "Dex connectors, SSO providers, SCIM, and password policy.",
        icon: ShieldAlert,
      },
      {
        href: "/dashboard/settings/group-mappings",
        title: "Group mappings",
        description: "Map identity-provider groups to scoped RBAC roles.",
        icon: Users,
      },
    ],
  },
  {
    label: "Governance",
    description:
      "Define tenancy, policy, evidence, and organizational boundaries.",
    items: [
      {
        href: "/dashboard/settings/quotas",
        title: "Quota plans",
        description:
          "Set per-tenant limits for projects, clusters, storage, and tokens.",
        icon: Gauge,
      },
      {
        href: "/dashboard/settings/cluster-groups",
        title: "Cluster groups",
        description:
          "Organize clusters by environment, region, or business unit.",
        icon: FolderTree,
      },
      {
        href: "/dashboard/settings/network-policies",
        title: "Network policy templates",
        description: "Curate reusable isolation policies for adopted clusters.",
        icon: Network,
      },
      {
        href: "/dashboard/settings/read-audit",
        title: "Read-audit policies",
        description: "Choose which sensitive reads produce evidence records.",
        icon: FileSearch,
      },
      {
        href: "/dashboard/settings/compliance",
        title: "Compliance exports",
        description:
          "Create signed audit, RBAC, and configuration evidence bundles.",
        icon: FileArchive,
      },
    ],
  },
  {
    label: "Reliability",
    description: "Operate the management plane and its shared services.",
    items: [
      {
        href: "/dashboard/settings/operations",
        title: "Operations",
        description:
          "Inspect worker queues, retry failed work, and manage the DLQ.",
        icon: Activity,
      },
      {
        href: "/dashboard/settings/backup",
        title: "Astronomer backup",
        description:
          "Configure management-plane backups, encryption, and restore drills.",
        icon: ShieldCheck,
      },
      {
        href: "/dashboard/settings/monitoring",
        title: "Shared stacks",
        description: "Operate shared Thanos and Alertmanager installations.",
        icon: BarChart3,
      },
    ],
  },
  {
    label: "Integrations",
    description:
      "Connect delivery, notifications, secrets, and external systems.",
    items: [
      {
        href: "/dashboard/settings/gitops",
        title: "GitOps sources",
        description: "Register and secure Flux-native repository sources.",
        icon: FolderGit2,
      },
      {
        href: "/dashboard/settings/smtp",
        title: "Email & SMTP",
        description:
          "Configure outbound mail, test delivery, and inspect sent mail.",
        icon: Mail,
      },
      {
        href: "/dashboard/settings/webhooks",
        title: "Webhooks",
        description:
          "Deliver platform events to incident and automation systems.",
        icon: Webhook,
      },
      {
        href: "/dashboard/settings/templates",
        title: "Notification templates",
        description: "Customize transactional email and webhook messages.",
        icon: FileText,
      },
      {
        href: "/dashboard/settings/siem",
        title: "SIEM forwarders",
        description: "Stream audit and platform events to security tooling.",
        icon: Radio,
      },
      {
        href: "/dashboard/settings/vault",
        title: "Vault connections",
        description: "Resolve encrypted Vault references during installations.",
        icon: KeyRound,
      },
      {
        href: "/dashboard/extensions",
        title: "Extensions",
        description: "Review extension manifests, permissions, and enablement.",
        icon: Puzzle,
        featureFlag: "feature.extensions",
      },
    ],
  },
] as const;

export type SettingsNavigation = typeof SETTINGS_NAVIGATION;
export type SettingsNavigationGroupEntry = SettingsNavigation[number];

export function visibleSettingsNavigation(
  groups: SettingsNavigation,
  options: {
    isSuperuser: boolean;
    canManageCharlie: boolean;
    extensionsEnabled: boolean;
  },
): SettingsNavigationGroupEntry[] {
  const visible: SettingsNavigationGroupEntry[] = [];
  for (const group of groups) {
    const items = group.items.filter((item) => {
      if (item.featureFlag && !options.extensionsEnabled) return false;
      if (options.isSuperuser) {
        return !item.charlie || options.canManageCharlie;
      }
      return item.charlie && options.canManageCharlie;
    });
    if (items.length > 0) visible.push({ ...group, items });
  }
  return visible;
}

export function settingsItemIsActive(pathname: string, href: string): boolean {
  return pathname === href || pathname.startsWith(`${href}/`);
}
