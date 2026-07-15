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
    href: '/dashboard/extensions',
    title: 'Extensions',
    description: 'Manifest validation, permissions review, and enablement controls.',
    icon: Puzzle,
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
    href: '/dashboard/settings/network-policies',
    title: 'Network policy templates',
    description: 'Deny-all / project-isolated / namespace-only Kubernetes NetworkPolicy bundles.',
    icon: Network,
  },
];

function SettingsHubPage() {
  // Every card fans out to an admin-only surface guarded by SettingsAuthGate.
  // Rather than showing all 17 cards to a non-admin who'd bounce off a 403 per
  // click (afford-then-deny, F-06), show a single "Admins only" state. Direct
  // deep-links still work — each subpage's SettingsAuthGate enforces access.
  const { isSuperuser, ready } = useIsSuperuser();

  // While auth hydrates (!ready) render the header only — don't flash the full
  // 17-card grid to a user who will turn out to be a non-admin. Once ready, a
  // non-superuser gets a single "Admins only" state instead of afford-then-deny.
  if (!ready || !isSuperuser) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Settings</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Platform configuration and administration.
          </p>
        </div>
        {ready && !isSuperuser && (
          <PermissionState
            title="Admins only"
            description="Platform settings are restricted to superusers. Contact an administrator if you need access."
          />
        )}
      </div>
    );
  }

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

      {/* §HostMounts mount point 4 — enabled `settingsPage` extensions append
          here. Renders nothing when no extension declares a settings point. */}
      <ExtensionSlot
        point="settingsPage"
        className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3"
      />
    </div>
  );
}

export const Route = createFileRoute('/dashboard/settings/')({
  component: SettingsHubPage,
});
