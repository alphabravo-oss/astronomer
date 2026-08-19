import { createFileRoute } from '@tanstack/react-router';
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
import { Link } from '@/lib/link';
import { ExtensionSlot } from '@/components/extensions/ExtensionSlot';
import { useIsSuperuser } from '@/components/settings/hooks';
import { PermissionState } from '@/components/ui/empty-state';
import { PageHeader, PageShell } from '@/components/ui/page';
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
  Activity,
  Puzzle,
  Radio,
  BarChart3,
  Sparkles,
} from 'lucide-react';
import { useFeatureFlags } from '@/lib/hooks';
import { useAuthStore } from '@/lib/store';
import { canManageCharlie as hasCharlieManagement } from '@/components/charlie/admin-utils';

interface SettingsCard {
  href: string;
  title: string;
  description: string;
  icon: React.ElementType;
  charlie?: boolean;
  featureFlag?: 'feature.extensions';
}

const CARDS: SettingsCard[] = [
  {
    href: '/dashboard/settings/charlie',
    title: 'Charlie',
    description: 'Connect and govern Charlie AI, its product agent, automation, access, and diagnostics.',
    icon: Sparkles,
    charlie: true,
  },
  {
    href: '/dashboard/settings/platform',
    title: 'Platform',
    description: 'Branding, banners, feature flags, token TTL, telemetry.',
    icon: Palette,
  },
  {
    href: '/dashboard/settings/operations',
    title: 'Operations',
    description: 'Worker queues + DLQ inspection with retry / discard actions.',
    icon: Activity,
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
    href: '/dashboard/settings/backup',
    title: 'Astronomer backup',
    description: 'Nightly management-plane dump, destination, encryption-key wrapping, restore drill.',
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
    href: '/dashboard/extensions',
    title: 'Extensions',
    description: 'Manifest validation, permissions review, and enablement controls.',
    icon: Puzzle,
    featureFlag: 'feature.extensions',
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
    href: '/dashboard/settings/siem',
    title: 'SIEM forwarders',
    description: 'Stream audit + platform events to syslog / Splunk HEC / NDJSON-HTTPS.',
    icon: Radio,
  },
  {
    href: '/dashboard/settings/monitoring',
    title: 'Shared observability stacks',
    description: 'Shared Thanos + Alertmanager: install, upgrade, replace, uninstall.',
    icon: BarChart3,
  },
  {
    href: '/dashboard/settings/network-policies',
    title: 'Network policy templates',
    description: 'Deny-all / project-isolated / namespace-only Kubernetes NetworkPolicy bundles.',
    icon: Network,
  },
];

function SettingsHubPage() {
  // Most cards are superuser-only. Charlie is the deliberate exception: its
  // global charlie:manage grant exposes exactly that one card even while the
  // integration is disabled, because enablement begins from local settings.
  const { isSuperuser, ready } = useIsSuperuser();
  const { data: featureFlags } = useFeatureFlags();
  const user = useAuthStore((state) => state.user);
  const canManageCharlie = hasCharlieManagement(user);

  // While auth hydrates (!ready) render the header only — don't flash the full
  // grid to a user who will turn out to lack all administration access.
  if (!ready || (!isSuperuser && featureFlags === undefined)) {
    return (
      <PageShell>
        <PageHeader
          title="Settings"
          description="Platform configuration and administration."
        />
      </PageShell>
    );
  }

  if (!isSuperuser && !canManageCharlie) {
    return (
      <PageShell>
        <PageHeader
          title="Settings"
          description="Platform configuration and administration."
        />
        <PermissionState
          title="Administration permission required"
          description="Platform settings require superuser access, or charlie:manage for the Charlie administration surface."
        />
      </PageShell>
    );
  }

  return (
    <PageShell>
      <PageHeader
        title="Settings"
        description="Platform configuration and administration. All surfaces below are admin-only."
      />

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {CARDS.filter((card) => {
          if (card.featureFlag && featureFlags?.[card.featureFlag] !== true) return false;
          return isSuperuser
            ? (!card.charlie || canManageCharlie)
            : card.charlie && canManageCharlie;
        }).map((card) => {
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

      {/* §HostMounts mount point 4 — enabled `settingsPage` extensions append
          here. Renders nothing when no extension declares a settings point. */}
      <ExtensionSlot
        point="settingsPage"
        className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3"
      />
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/settings/')({
  component: SettingsHubPage,
});
