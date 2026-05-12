'use client';

/**
 * Settings hub landing — a card grid that fans out to the per-area subpages
 * shipped in sprints 9–14. The original tabbed settings UI moved to
 * `/dashboard/settings/general/`; this page replaces the index with a
 * navigation surface so each surface gets a dedicated route and URL.
 *
 * Every link is admin-only; the per-page `SettingsAuthGate` enforces it.
 * Showing the cards to non-admins is still fine — they'll just bounce off a
 * 403 placeholder on click.
 */
import Link from 'next/link';
import {
  Palette,
  Mail,
  Webhook,
  Gauge,
  Users,
  FileArchive,
  ShieldCheck,
  Settings as SettingsIcon,
  ShieldAlert,
  LayoutDashboard,
  FileText,
  FileSearch,
  Network,
  FolderTree,
  KeyRound,
} from 'lucide-react';

interface SettingsCard {
  href: string;
  title: string;
  description: string;
  icon: React.ElementType;
}

const CARDS: SettingsCard[] = [
  {
    href: '/dashboard/settings/platform',
    title: 'Platform',
    description: 'Branding, banners, feature flags, token TTL, telemetry.',
    icon: Palette,
  },
  {
    href: '/dashboard/settings/smtp',
    title: 'Email & SMTP',
    description: 'Outbound mail server, test sends, sent-email audit log.',
    icon: Mail,
  },
  {
    href: '/dashboard/settings/webhooks',
    title: 'Webhooks',
    description: 'Slack / PagerDuty / generic event subscribers + deliveries.',
    icon: Webhook,
  },
  {
    href: '/dashboard/settings/templates',
    title: 'Notification templates',
    description: 'Customize subject + body of every transactional email + webhook.',
    icon: FileText,
  },
  {
    href: '/dashboard/settings/quotas',
    title: 'Quota plans',
    description: 'Per-tenant caps on projects, clusters, storage, tokens.',
    icon: Gauge,
  },
  {
    href: '/dashboard/settings/group-mappings',
    title: 'Group mappings',
    description: 'SSO group → RBAC role bindings, with optional scoping.',
    icon: Users,
  },
  {
    href: '/dashboard/settings/cluster-groups',
    title: 'Cluster groups',
    description: 'Organize clusters into folders by environment, region, or BU.',
    icon: FolderTree,
  },
  {
    href: '/dashboard/settings/compliance',
    title: 'Compliance exports',
    description: 'Build a signed ZIP of audit + RBAC + config for a date range.',
    icon: FileArchive,
  },
  {
    href: '/dashboard/settings/backup-drill',
    title: 'Backup drill',
    description: 'Latest restore drill result + history.',
    icon: ShieldCheck,
  },
  {
    href: '/dashboard/settings/auth',
    title: 'Authentication',
    description: 'Dex connectors, SSO providers, password policy.',
    icon: ShieldAlert,
  },
  {
    href: '/dashboard/settings/widgets',
    title: 'Dashboard widgets',
    description: 'Prometheus sparklines, Grafana panels, and URL iframes pinned to dashboards.',
    icon: LayoutDashboard,
  },
  {
    href: '/dashboard/settings/vault',
    title: 'Vault connections',
    description: 'HashiCorp Vault sources for ${vault://...} install-time secret refs.',
    icon: KeyRound,
  },
  {
    href: '/dashboard/settings/general',
    title: 'General',
    description: 'Platform name, audit logging, API tokens, support bundle.',
    icon: SettingsIcon,
  },
  {
    href: '/dashboard/settings/read-audit',
    title: 'Read-audit policies',
    description: 'Which GET endpoints emit a "who saw what credential" audit row.',
    icon: FileSearch,
  },
  {
    href: '/dashboard/settings/network-policies',
    title: 'Network policy templates',
    description: 'Deny-all / project-isolated / namespace-only Kubernetes NetworkPolicy bundles.',
    icon: Network,
  },
];

export default function SettingsHubPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Platform configuration and administration. All surfaces below are admin-only.
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {CARDS.map((card) => {
          const Icon = card.icon;
          return (
            <Link
              key={card.href}
              href={card.href}
              className="flex flex-col gap-2 p-4 rounded-lg border border-border bg-card text-left hover:bg-card/80 hover:border-foreground/20 transition-colors"
            >
              <div className="flex items-center gap-2">
                <div className="flex-shrink-0 w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
                  <Icon className="h-4 w-4 text-foreground" />
                </div>
                <p className="text-sm font-medium text-foreground">{card.title}</p>
              </div>
              <p className="text-xs text-muted-foreground line-clamp-2">{card.description}</p>
            </Link>
          );
        })}
      </div>
    </div>
  );
}
