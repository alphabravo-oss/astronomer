'use client';

/**
 * /dashboard/settings/auth/ — overview page.
 *
 * Three concerns:
 *   1. Identity Broker card — shows whether the `dex` cluster tool is installed
 *      anywhere. We piggy-back on /tools and /clusters/{id}/tools/status; no
 *      new endpoint required. When Dex isn't found, we link to the install
 *      flow. When it is, we link to the settings page.
 *   2. Configured connectors table — DataTable over /auth/dex/connectors/.
 *      Row actions: edit, delete (with ConfirmDialog), apply.
 *   3. Register-as-SSO callout — only after at least one connector exists,
 *      because there's no point flipping the SSO row on without an upstream.
 */
import { useMemo, useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import {
  Plus,
  ShieldCheck,
  Wrench,
  RefreshCw,
  Trash2,
  Pencil,
  ArrowRight,
  KeyRound,
} from 'lucide-react';
import { useTools, useClusterToolsStatus, useClusters } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { ActionMenu } from '@/components/ui/action-menu';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import {
  useDexConnectors,
  useDeleteDexConnector,
  useApplyDexConfig,
  useDexSettings,
} from '@/components/auth/hooks';
import { getConnectorMeta } from '@/components/auth/connector-meta';
import type { DexConnector } from '@/types';

export default function AuthOverviewPage() {
  const router = useRouter();
  const { data: connectors = [], isLoading: connectorsLoading } = useDexConnectors();
  const { data: settings } = useDexSettings();
  const deleteMutation = useDeleteDexConnector();
  const applyMutation = useApplyDexConfig();

  const [deleteTarget, setDeleteTarget] = useState<DexConnector | null>(null);

  // Dex install detection: walk every cluster's tool status, looking for the
  // `dex` slug. We don't need a new endpoint — this matches how the cluster
  // tools page itself decides what to render.
  const dexInstall = useDexInstallStatus();

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteMutation.mutateAsync(deleteTarget.id);
      setDeleteTarget(null);
    } catch {
      /* mutation toasts on error */
    }
  };

  const columns: Column<DexConnector>[] = [
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => {
        const meta = getConnectorMeta(row.type);
        const Icon = meta.icon;
        return (
          <div className="flex items-center gap-2">
            <Icon className="h-4 w-4 text-muted-foreground flex-shrink-0" />
            <span className="text-sm text-foreground">{meta.label || row.type}</span>
          </div>
        );
      },
      sortAccessor: (row) => row.type,
    },
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.name}</span>,
      sortAccessor: (row) => row.name,
    },
    {
      key: 'displayName',
      header: 'Display Name',
      accessor: (row) => <span className="text-sm text-foreground">{row.displayName || '—'}</span>,
      sortAccessor: (row) => row.displayName,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
          size="sm"
        />
      ),
      sortAccessor: (row) => (row.enabled ? '1' : '0'),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      align: 'center',
      accessor: (row) => (
        <ActionMenu
          items={[
            {
              label: 'Edit',
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => router.push(`/dashboard/settings/auth/connectors/${row.id}`),
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: 'destructive',
              separator: true,
            },
          ]}
        />
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Settings · Auth
          </p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
            Identity Broker
          </h1>
          <p className="text-sm text-muted-foreground mt-1 max-w-2xl">
            Astronomer brokers enterprise IdPs through Dex. Configure upstream connectors
            (Azure AD, Okta, LDAP, SAML, …) here; once applied, register Dex as the
            platform's SSO provider with one click.
          </p>
        </div>
        {connectors.length > 0 && (
          <Link
            href="/dashboard/settings/auth/register-sso"
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity"
          >
            <ShieldCheck className="h-4 w-4" />
            Register Dex as SSO
          </Link>
        )}
      </div>

      {/* Identity broker card */}
      <DexInstallCard
        installed={dexInstall.installed}
        clusterName={dexInstall.clusterName}
        loading={dexInstall.loading}
        issuerUrl={settings?.issuerUrl}
      />

      {/* Connector table */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-base font-semibold text-foreground">Configured Connectors</h2>
            <p className="text-xs text-muted-foreground mt-0.5">
              Each row becomes a `connectors` entry in the rendered Dex config.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => applyMutation.mutate()}
              disabled={applyMutation.isPending || connectors.length === 0}
              className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
              title="Reconcile the retained runtime Secret and roll Dex when changed"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${applyMutation.isPending ? 'animate-spin' : ''}`} />
              Apply to Dex
            </button>
            <Link
              href="/dashboard/settings/auth/connectors/new"
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Add Connector
            </Link>
          </div>
        </div>

        <DataTable
          data={connectors}
          columns={columns}
          keyExtractor={(row) => row.id}
          searchPlaceholder="Search connectors..."
          loading={connectorsLoading}
          emptyMessage="No connectors configured. Add one to broker an upstream IdP."
        />
      </div>

      {/* Quick links */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <Link
          href="/dashboard/settings/auth/settings"
          className="flex items-center gap-3 p-4 rounded-lg border border-border bg-card hover:bg-card/80 transition-colors"
        >
          <div className="flex-shrink-0 w-9 h-9 rounded-lg bg-muted flex items-center justify-center">
            <KeyRound className="h-4 w-4 text-muted-foreground" />
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">Dex Settings</p>
            <p className="text-xs text-muted-foreground">Issuer URL, public clients, token expiry.</p>
          </div>
          <ArrowRight className="h-4 w-4 text-muted-foreground" />
        </Link>
        <Link
          href="/dashboard/settings/auth/register-sso"
          className="flex items-center gap-3 p-4 rounded-lg border border-border bg-card hover:bg-card/80 transition-colors"
        >
          <div className="flex-shrink-0 w-9 h-9 rounded-lg bg-muted flex items-center justify-center">
            <ShieldCheck className="h-4 w-4 text-muted-foreground" />
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">Register as SSO</p>
            <p className="text-xs text-muted-foreground">Wire Dex as the platform's OIDC SSO provider.</p>
          </div>
          <ArrowRight className="h-4 w-4 text-muted-foreground" />
        </Link>
        <Link
          href="/dashboard/settings/auth/scim-tokens"
          className="flex items-center gap-3 p-4 rounded-lg border border-border bg-card hover:bg-card/80 transition-colors"
        >
          <div className="flex-shrink-0 w-9 h-9 rounded-lg bg-muted flex items-center justify-center">
            <KeyRound className="h-4 w-4 text-muted-foreground" />
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-foreground">SCIM Provisioning Tokens</p>
            <p className="text-xs text-muted-foreground">Mint / revoke bearer tokens for IdP SCIM 2.0 sync.</p>
          </div>
          <ArrowRight className="h-4 w-4 text-muted-foreground" />
        </Link>
      </div>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Delete connector"
        description={`This will remove the "${deleteTarget?.name}" connector. Apply the changes to Dex afterwards to roll out the update.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}

// ============================================================
// Dex install detection
// ============================================================

interface DexInstallStatus {
  installed: boolean;
  clusterName?: string;
  loading: boolean;
}

function useDexInstallStatus(): DexInstallStatus {
  const { data: tools } = useTools();
  const { data: clustersData, isLoading: clustersLoading } = useClusters({ pageSize: 100 });
  const clusters = clustersData?.data ?? [];

  // Look for the dex tool slug — early-return when the registry doesn't even
  // have it (so the card can advertise "Dex isn't packaged" instead of
  // misleading the operator).
  const dexTool = useMemo(() => tools?.find((t) => t.slug === 'dex'), [tools]);

  // We can only inspect cluster status one cluster at a time. Use the first
  // cluster as the "primary" — for management-cluster setups this is normally
  // the only one. The `useClusterToolsStatus` hook short-circuits when the
  // id is empty, so this is cheap when there are no clusters yet.
  const primary = clusters[0];
  const { data: statuses, isLoading: statusLoading } = useClusterToolsStatus(primary?.id ?? '');

  const installed = useMemo(() => {
    if (!statuses) return false;
    return statuses.some(
      (s) => s.slug === 'dex' && (s.status === 'installed' || s.status === 'installed_unmanaged'),
    );
  }, [statuses]);

  return {
    installed: !!dexTool && installed,
    clusterName: primary?.displayName || primary?.name,
    loading: clustersLoading || statusLoading,
  };
}

function DexInstallCard({
  installed,
  clusterName,
  loading,
  issuerUrl,
}: {
  installed: boolean;
  clusterName?: string;
  loading: boolean;
  issuerUrl?: string;
}) {
  return (
    <div className="rounded-xl border border-border bg-card p-5">
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-3 min-w-0">
          <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
            <Wrench className="h-5 w-5 text-muted-foreground" />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <p className="text-sm font-semibold text-foreground">Dex</p>
              <StatusBadge
                status={installed ? 'active' : loading ? 'connecting' : 'disconnected'}
                label={installed ? 'Installed' : loading ? 'Checking…' : 'Not installed'}
                size="sm"
              />
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              {installed
                ? `Running on ${clusterName ?? 'the management cluster'}.`
                : 'Dex brokers messy upstream IdPs into a single OIDC issuer Astronomer can register.'}
            </p>
            {issuerUrl && (
              <p className="text-2xs font-mono text-muted-foreground mt-1.5 truncate">
                Issuer · {issuerUrl}
              </p>
            )}
          </div>
        </div>
        <div className="flex-shrink-0">
          {installed ? (
            <Link
              href="/dashboard/settings/auth/settings"
              className="inline-flex items-center gap-2 h-8 px-3 rounded-lg border border-border text-xs
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              Configure
            </Link>
          ) : (
            <Link
              href="/dashboard/settings/auth/install"
              className="inline-flex items-center gap-2 h-8 px-3 rounded-lg bg-primary text-primary-foreground
                text-xs font-medium hover:opacity-90 transition-opacity"
            >
              Install Dex
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
