'use client';

import { useState, useEffect } from 'react';
import { useTabParam } from '@/lib/use-tab-param';
import { useSSOProviders, useAPITokens, useCreateAPIToken, useDeleteAPIToken, useAuditLogs, useGeneralSettings, useSaveGeneralSettings, useCreateSSOProvider } from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { CodeBlock } from '@/components/ui/code-block';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { formatRelativeTime, formatDate, cn } from '@/lib/utils';
import type { SSOProvider, APIToken, AuditLogEntry } from '@/types';
import {
  Settings,
  Shield,
  Key,
  FileText,
  Plus,
  Trash2,
  Loader2,
  X,
  Github,
  Chrome,
  KeyRound,
  Lock,
  LifeBuoy,
  Download,
} from 'lucide-react';
import { toastError, toastSuccess } from '@/lib/toast';

type TabKey = 'sso' | 'general' | 'tokens' | 'audit' | 'support';

const TAB_KEYS = ['sso', 'general', 'tokens', 'audit', 'support'] as const;

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'sso', label: 'SSO Providers', icon: Shield },
  { key: 'general', label: 'General', icon: Settings },
  { key: 'tokens', label: 'API Tokens', icon: Key },
  { key: 'audit', label: 'Audit Log', icon: FileText },
  { key: 'support', label: 'Support', icon: LifeBuoy },
];

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'sso');
  const [showCreateToken, setShowCreateToken] = useState(false);
  const [newTokenForm, setNewTokenForm] = useState({ name: '', description: '', expiresInDays: 30 });
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const [showAddSSO, setShowAddSSO] = useState(false);
  const [ssoForm, setSSOForm] = useState({
    type: 'github' as 'github' | 'google' | 'oidc',
    name: '',
    clientId: '',
    clientSecret: '',
    metadataUrl: '',
    allowedOrganizations: '',
    autoCreateUsers: true,
  });
  const [generalForm, setGeneralForm] = useState({
    platformName: 'Astronomer',
    agentHeartbeatInterval: 30,
    defaultSessionTimeout: 60,
    enableAuditLogging: true,
    metricsCollection: true,
  });

  const { data: generalSettings, isLoading: generalLoading } = useGeneralSettings();
  const saveGeneralSettings = useSaveGeneralSettings();
  const createSSOProvider = useCreateSSOProvider();

  useEffect(() => {
    if (generalSettings) {
      setGeneralForm({
        platformName: generalSettings.platformName ?? 'Astronomer',
        agentHeartbeatInterval: generalSettings.agentHeartbeatInterval ?? 30,
        defaultSessionTimeout: generalSettings.defaultSessionTimeout ?? 60,
        enableAuditLogging: generalSettings.enableAuditLogging ?? true,
        metricsCollection: generalSettings.metricsCollection ?? true,
      });
    }
  }, [generalSettings]);

  const { data: ssoProviders, isLoading: ssoLoading } = useSSOProviders();
  const { data: tokens, isLoading: tokensLoading } = useAPITokens();
  // Migration 063 — `action_class` filter for read-side audit rows.
  const [auditClassFilter, setAuditClassFilter] = useState<'all' | 'mutation' | 'read' | 'auth' | 'system'>('all');
  const { data: auditData, isLoading: auditLoading } = useAuditLogs({
    pageSize: 50,
    ...(auditClassFilter !== 'all' ? { action_class: auditClassFilter } : {}),
  }, {
    enabled: activeTab === 'audit',
  });
  const createToken = useCreateAPIToken();
  const deleteToken = useDeleteAPIToken();

  const auditLogs = auditData?.data || [];

  const handleCreateToken = async () => {
    if (!newTokenForm.name) {
      toastError('Token name is required');
      return;
    }
    try {
      const result = await createToken.mutateAsync({
        name: newTokenForm.name,
        description: newTokenForm.description || undefined,
        expiresInDays: newTokenForm.expiresInDays,
      });
      setCreatedToken(result.token);
    } catch {
      // Error handled by mutation
    }
  };

  const handleSaveGeneralSettings = async () => {
    try {
      await saveGeneralSettings.mutateAsync(generalForm);
    } catch {
      // Error is handled by the mutation's onError callback
    }
  };

  const handleCreateSSOProvider = async () => {
    if (!ssoForm.name) {
      toastError('Provider name is required');
      return;
    }
    if (!ssoForm.clientId) {
      toastError('Client ID is required');
      return;
    }
    try {
      await createSSOProvider.mutateAsync({
        type: ssoForm.type,
        name: ssoForm.name,
        enabled: true,
        config: {
          clientId: ssoForm.clientId,
          clientSecret: ssoForm.clientSecret || undefined,
          metadataUrl: ssoForm.metadataUrl || undefined,
          allowedOrganizations: ssoForm.allowedOrganizations || undefined,
          autoCreateUsers: ssoForm.autoCreateUsers,
        },
      });
      setShowAddSSO(false);
      setSSOForm({
        type: 'github',
        name: '',
        clientId: '',
        clientSecret: '',
        metadataUrl: '',
        allowedOrganizations: '',
        autoCreateUsers: true,
      });
    } catch {
      // Error is handled by the mutation's onError callback
    }
  };

  const providerIcon = (type: string) => {
    switch (type) {
      case 'github': return <Github className="h-5 w-5" />;
      case 'google': return <Chrome className="h-5 w-5" />;
      case 'oidc': return <KeyRound className="h-5 w-5" />;
      default: return <Shield className="h-5 w-5" />;
    }
  };

  const tokenColumns: Column<APIToken>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          {row.description && <p className="text-xs text-muted-foreground">{row.description}</p>}
        </div>
      ),
    },
    {
      key: 'prefix',
      header: 'Prefix',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.prefix}...</span>,
    },
    {
      key: 'createdBy',
      header: 'Created By',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.createdBy}</span>,
    },
    {
      key: 'expires',
      header: 'Expires',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.expiresAt ? formatDate(row.expiresAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'lastUsed',
      header: 'Last Used',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastUsedAt ? formatRelativeTime(row.lastUsedAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            if (confirm('Are you sure you want to delete this token?')) {
              deleteToken.mutate(row.id);
            }
          }}
          className="text-muted-foreground hover:text-status-error transition-colors"
          title="Delete token"
        >
          <Trash2 className="h-4 w-4" />
        </button>
      ),
    },
  ];

  const auditColumns: Column<AuditLogEntry>[] = [
    {
      key: 'timestamp',
      header: 'Timestamp',
      accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{formatDate(row.timestamp)}</span>,
    },
    {
      key: 'user',
      header: 'User',
      accessor: (row) => <span className="text-sm text-foreground">{row.user}</span>,
    },
    {
      key: 'action',
      header: 'Action',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">{row.action}</span>
      ),
    },
    {
      key: 'resource',
      header: 'Resource',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">
          {row.resourceType}/{row.resourceName}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge status={row.status === 'success' ? 'active' : 'error'} label={row.status} size="sm" />
      ),
    },
    {
      key: 'source',
      header: 'Source IP',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.sourceIP}</span>,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground mt-1">
          Platform configuration and administration
        </p>
      </div>

      {/* Tabs */}
      <div className="border-b border-border overflow-x-auto">
        <nav className="flex min-w-max gap-6">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            return (
              <button
                key={tab.key}
                onClick={() => setActiveTab(tab.key)}
                className={cn(
                  'flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors',
                  activeTab === tab.key
                    ? 'border-foreground text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground'
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </button>
            );
          })}
        </nav>
      </div>

      {/* Content */}
      <div className="animate-fade-in">
        {/* SSO Providers */}
        {activeTab === 'sso' && (
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">
              Configure Single Sign-On providers for your organization.
            </p>
            {ssoLoading ? (
              <div className="flex items-center justify-center h-32">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {(ssoProviders || []).map((provider) => (
                  <div
                    key={provider.id}
                    className="flex items-center gap-4 p-4 rounded-lg border border-border bg-card hover:bg-card/80 transition-colors"
                  >
                    <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center text-muted-foreground">
                      {providerIcon(provider.type)}
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="font-medium text-foreground">{provider.name}</p>
                      <p className="text-xs text-muted-foreground capitalize">{provider.type}</p>
                    </div>
                    <StatusBadge
                      status={provider.enabled ? 'active' : 'disconnected'}
                      label={provider.enabled ? 'Enabled' : 'Disabled'}
                      size="sm"
                    />
                  </div>
                ))}

                {/* Add Provider card */}
                <button
                  onClick={() => setShowAddSSO(true)}
                  className="flex items-center justify-center gap-2 p-4 rounded-lg border border-dashed border-border
                    text-muted-foreground hover:text-foreground hover:border-foreground/20 transition-colors"
                >
                  <Plus className="h-4 w-4" />
                  <span className="text-sm font-medium">Add SSO Provider</span>
                </button>
              </div>
            )}
          </div>
        )}

        {/* General Settings */}
        {activeTab === 'general' && (
          <div className="max-w-2xl space-y-6">
            {generalLoading ? (
              <div className="flex items-center justify-center h-32">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
              </div>
            ) : (
              <div className="space-y-4">
                <h3 className="text-lg font-medium text-foreground">Platform Settings</h3>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Platform Name</label>
                  <input
                    aria-label="Platform Name"
                    type="text"
                    value={generalForm.platformName}
                    onChange={(e) => setGeneralForm((f) => ({ ...f, platformName: e.target.value }))}
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Agent Heartbeat Interval</label>
                  <select
                    aria-label="Agent Heartbeat Interval"
                    value={generalForm.agentHeartbeatInterval}
                    onChange={(e) => setGeneralForm((f) => ({ ...f, agentHeartbeatInterval: Number(e.target.value) }))}
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      focus:outline-none focus:ring-2 focus:ring-ring"
                  >
                    <option value={15}>15 seconds</option>
                    <option value={30}>30 seconds</option>
                    <option value={60}>60 seconds</option>
                  </select>
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Default Session Timeout</label>
                  <select
                    aria-label="Default Session Timeout"
                    value={generalForm.defaultSessionTimeout}
                    onChange={(e) => setGeneralForm((f) => ({ ...f, defaultSessionTimeout: Number(e.target.value) }))}
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      focus:outline-none focus:ring-2 focus:ring-ring"
                  >
                    <option value={30}>30 minutes</option>
                    <option value={60}>1 hour</option>
                    <option value={480}>8 hours</option>
                    <option value={1440}>24 hours</option>
                  </select>
                </div>

                <div className="flex items-center justify-between p-4 rounded-lg border border-border">
                  <div>
                    <p className="text-sm font-medium text-foreground">Enable Audit Logging</p>
                    <p className="text-xs text-muted-foreground">Log all API actions for compliance</p>
                  </div>
                  <button
                    aria-label="Enable Audit Logging"
                    onClick={() => setGeneralForm((f) => ({ ...f, enableAuditLogging: !f.enableAuditLogging }))}
                    className={cn(
                      'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                      generalForm.enableAuditLogging ? 'bg-status-success' : 'bg-muted'
                    )}
                  >
                    <span className={cn(
                      'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
                      generalForm.enableAuditLogging ? 'translate-x-6' : 'translate-x-1'
                    )} />
                  </button>
                </div>

                <div className="flex items-center justify-between p-4 rounded-lg border border-border">
                  <div>
                    <p className="text-sm font-medium text-foreground">Metrics Collection</p>
                    <p className="text-xs text-muted-foreground">Collect and aggregate cluster metrics</p>
                  </div>
                  <button
                    aria-label="Metrics Collection"
                    onClick={() => setGeneralForm((f) => ({ ...f, metricsCollection: !f.metricsCollection }))}
                    className={cn(
                      'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                      generalForm.metricsCollection ? 'bg-status-success' : 'bg-muted'
                    )}
                  >
                    <span className={cn(
                      'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
                      generalForm.metricsCollection ? 'translate-x-6' : 'translate-x-1'
                    )} />
                  </button>
                </div>
              </div>
            )}

            <button
              onClick={handleSaveGeneralSettings}
              disabled={saveGeneralSettings.isPending}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {saveGeneralSettings.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Save Settings
            </button>
          </div>
        )}

        {/* API Tokens */}
        {activeTab === 'tokens' && (
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <p className="text-sm text-muted-foreground">
                API tokens for programmatic access to the Astronomer API.
              </p>
              <button
                onClick={() => {
                  setShowCreateToken(true);
                  setCreatedToken(null);
                  setNewTokenForm({ name: '', description: '', expiresInDays: 30 });
                }}
                className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                  text-sm font-medium hover:opacity-90 transition-opacity"
              >
                <Plus className="h-4 w-4" />
                Create Token
              </button>
            </div>

            <DataTable
              data={tokens || []}
              columns={tokenColumns}
              keyExtractor={(row) => row.id}
              searchPlaceholder="Search tokens..."
              loading={tokensLoading}
              emptyMessage="No API tokens created"
            />
          </div>
        )}

        {/* Audit Log */}
        {activeTab === 'audit' && (
          <div className="space-y-3">
            {/* action_class filter (migration 063). "all" leaves the
                query unfiltered. "read" surfaces credential-read audit
                rows specifically; "mutation" hides the read-side noise. */}
            <div className="flex items-center gap-3">
              <label className="text-xs uppercase tracking-wide text-muted-foreground">
                Class
              </label>
              <select
                value={auditClassFilter}
                onChange={(e) => setAuditClassFilter(e.target.value as typeof auditClassFilter)}
                className="rounded-md border border-border bg-background text-sm px-2 py-1"
              >
                <option value="all">All</option>
                <option value="mutation">Mutation</option>
                <option value="read">Read (credential view)</option>
                <option value="auth">Auth</option>
                <option value="system">System</option>
              </select>
            </div>
            <DataTable
              data={auditLogs}
              columns={auditColumns}
              keyExtractor={(row) => row.id}
              searchPlaceholder="Search audit logs..."
              loading={auditLoading}
              emptyMessage="No audit log entries"
              pageSize={25}
            />
          </div>
        )}

        {/* Support */}
        {activeTab === 'support' && <SupportTab />}
      </div>

      {/* Add SSO Provider Modal */}
      {showAddSSO && (
        <OverlayShell onClose={() => setShowAddSSO(false)}>
          <div className="relative mx-4 w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl p-6 space-y-5">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-semibold text-foreground">Add SSO Provider</h3>
              <button
                onClick={() => setShowAddSSO(false)}
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                <X className="h-5 w-5" />
              </button>
            </div>

            <div className="space-y-4">
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Provider Type</label>
                <select
                  value={ssoForm.type}
                  onChange={(e) => setSSOForm((f) => ({ ...f, type: e.target.value as 'github' | 'google' | 'oidc' }))}
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    focus:outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="github">GitHub</option>
                  <option value="google">Google</option>
                  <option value="oidc">OIDC</option>
                </select>
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Provider Name</label>
                <input
                  type="text"
                  value={ssoForm.name}
                  onChange={(e) => setSSOForm((f) => ({ ...f, name: e.target.value }))}
                  placeholder="e.g., Corporate GitHub"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  autoFocus
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Client ID</label>
                <input
                  type="text"
                  value={ssoForm.clientId}
                  onChange={(e) => setSSOForm((f) => ({ ...f, clientId: e.target.value }))}
                  placeholder="OAuth client ID"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Client Secret</label>
                <input
                  type="password"
                  value={ssoForm.clientSecret}
                  onChange={(e) => setSSOForm((f) => ({ ...f, clientSecret: e.target.value }))}
                  placeholder="OAuth client secret"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>

              {ssoForm.type === 'oidc' && (
                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">
                    Discovery URL
                  </label>
                  <input
                    type="text"
                    value={ssoForm.metadataUrl}
                    onChange={(e) => setSSOForm((f) => ({ ...f, metadataUrl: e.target.value }))}
                    placeholder="https://idp.example.com/.well-known/openid-configuration"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </div>
              )}

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Allowed Organizations</label>
                <input
                  type="text"
                  value={ssoForm.allowedOrganizations}
                  onChange={(e) => setSSOForm((f) => ({ ...f, allowedOrganizations: e.target.value }))}
                  placeholder="Comma-separated list (optional)"
                  className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>

              <div className="flex items-center justify-between p-3 rounded-lg border border-border">
                <div>
                  <p className="text-sm font-medium text-foreground">Auto-create Users</p>
                  <p className="text-xs text-muted-foreground">Automatically create accounts on first login</p>
                </div>
                <button
                  onClick={() => setSSOForm((f) => ({ ...f, autoCreateUsers: !f.autoCreateUsers }))}
                  className={cn(
                    'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                    ssoForm.autoCreateUsers ? 'bg-status-success' : 'bg-muted'
                  )}
                >
                  <span className={cn(
                    'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
                    ssoForm.autoCreateUsers ? 'translate-x-6' : 'translate-x-1'
                  )} />
                </button>
              </div>

              <div className="flex justify-end gap-2 pt-2">
                <button
                  onClick={() => setShowAddSSO(false)}
                  className="h-9 px-4 rounded-lg border border-border text-sm font-medium
                    text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                >
                  Cancel
                </button>
                <button
                  onClick={handleCreateSSOProvider}
                  disabled={!ssoForm.name || !ssoForm.clientId || createSSOProvider.isPending}
                  className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                    text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
                >
                  {createSSOProvider.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  Add Provider
                </button>
              </div>
            </div>
          </div>
        </OverlayShell>
      )}

      {/* Create Token Modal */}
      {showCreateToken && (
        <OverlayShell onClose={() => setShowCreateToken(false)}>
          <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl p-6 space-y-5">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-semibold text-foreground">
                {createdToken ? 'Token Created' : 'Create API Token'}
              </h3>
              <button
                onClick={() => setShowCreateToken(false)}
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                <X className="h-5 w-5" />
              </button>
            </div>

            {createdToken ? (
              <div className="space-y-4">
                <p className="text-sm text-status-warning">
                  Copy this token now. You will not be able to see it again.
                </p>
                <CodeBlock code={createdToken} title="API Token" />
                <button
                  onClick={() => setShowCreateToken(false)}
                  className="w-full h-9 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
                >
                  Done
                </button>
              </div>
            ) : (
              <div className="space-y-4">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Token Name</label>
                  <input
                    type="text"
                    value={newTokenForm.name}
                    onChange={(e) => setNewTokenForm((f) => ({ ...f, name: e.target.value }))}
                    placeholder="e.g., CI/CD Pipeline"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                    autoFocus
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Description (optional)</label>
                  <input
                    type="text"
                    value={newTokenForm.description}
                    onChange={(e) => setNewTokenForm((f) => ({ ...f, description: e.target.value }))}
                    placeholder="What is this token for?"
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-foreground">Expires In</label>
                  <select
                    value={newTokenForm.expiresInDays}
                    onChange={(e) => setNewTokenForm((f) => ({ ...f, expiresInDays: Number(e.target.value) }))}
                    className="w-full h-10 px-3 rounded-lg border border-border bg-background text-sm
                      focus:outline-none focus:ring-2 focus:ring-ring"
                  >
                    <option value={7}>7 days</option>
                    <option value={30}>30 days</option>
                    <option value={90}>90 days</option>
                    <option value={365}>1 year</option>
                    <option value={0}>Never</option>
                  </select>
                </div>

                <div className="flex justify-end gap-2 pt-2">
                  <button
                    onClick={() => setShowCreateToken(false)}
                    className="h-9 px-4 rounded-lg border border-border text-sm font-medium
                      text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleCreateToken}
                    disabled={!newTokenForm.name || createToken.isPending}
                    className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                      text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
                  >
                    {createToken.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                    Create Token
                  </button>
                </div>
              </div>
            )}
          </div>
        </OverlayShell>
      )}
    </div>
  );
}

// SupportTab renders the "Download support bundle" button. The bundle
// itself is a streaming zip from /api/v1/support-bundle/; superusers only.
// Errors are surfaced via toast.
function SupportTab() {
  const [downloading, setDownloading] = useState(false);

  const handleDownload = async () => {
    setDownloading(true);
    try {
      // Use the shared axios instance so the JWT/auth interceptor stamps
      // the request; force a binary response so axios doesn't try to JSON-
      // decode the zip stream.
      const { default: api } = await import('@/lib/api');
      const res = await api.get('/support-bundle', { responseType: 'blob', timeout: 120000 });
      const blob = new Blob([res.data], { type: 'application/zip' });
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      // Server already proposes a filename via Content-Disposition; if axios
      // didn't surface it, fall back to a sane default.
      const disposition = res.headers?.['content-disposition'] || '';
      const match = /filename="([^"]+)"/.exec(disposition);
      link.download = match?.[1] || `astronomer-support-bundle-${Date.now()}.zip`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      toastSuccess('Support bundle downloaded');
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to download support bundle';
      toastError(message);
    } finally {
      setDownloading(false);
    }
  };

  return (
    <div className="max-w-2xl space-y-6">
      <div className="rounded-lg border border-border bg-card p-6 space-y-4">
        <div className="flex items-start gap-3">
          <LifeBuoy className="h-5 w-5 text-muted-foreground flex-shrink-0 mt-0.5" />
          <div className="space-y-1">
            <h3 className="text-sm font-semibold text-foreground">Support bundle</h3>
            <p className="text-sm text-muted-foreground">
              Downloads a zip with platform metadata, cluster rows, recent audit
              log entries, and the last 200 lines of logs from each
              control-plane pod. Useful when filing a bug or escalating to
              support.
            </p>
            <p className="text-xs text-muted-foreground">
              Passwords, CA certs, encrypted tokens, credential-shaped values,
              and sensitive pod log lines are redacted. Share the bundle only
              with people authorized to triage this install.
            </p>
          </div>
        </div>
        <button
          onClick={handleDownload}
          disabled={downloading}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors disabled:opacity-50"
        >
          {downloading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
          Download support bundle
        </button>
      </div>
    </div>
  );
}
