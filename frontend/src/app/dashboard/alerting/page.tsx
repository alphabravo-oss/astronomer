'use client';

import { useState } from 'react';
import {
  useAlertRules,
  useCreateAlertRule,
  useUpdateAlertRule,
  useDeleteAlertRule,
  useAlertEvents,
  useAcknowledgeAlert,
  useResolveAlert,
  useNotificationChannels,
  useCreateNotificationChannel,
  useTestNotificationChannel,
  useAlertSilences,
  useCreateAlertSilence,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import type {
  AlertRule,
  AlertEvent,
  NotificationChannel,
  AlertSilence,
  AlertSeverity,
  NotificationChannelType,
} from '@/types';
import {
  Bell,
  Plus,
  AlertTriangle,
  AlertCircle,
  Info,
  Shield,
  VolumeX,
  X,
  Loader2,
  Trash2,
  Pencil,
  Check,
  CheckCircle,
  Send,
  Hash,
  Mail,
  Webhook,
  MessageSquare,
} from 'lucide-react';

type TabKey = 'rules' | 'active' | 'channels' | 'silences';

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'rules', label: 'Alert Rules', icon: Shield },
  { key: 'active', label: 'Active Alerts', icon: AlertTriangle },
  { key: 'channels', label: 'Notification Channels', icon: Bell },
  { key: 'silences', label: 'Silences', icon: VolumeX },
];

const severityColors: Record<string, string> = {
  critical: 'bg-status-error/10 text-status-error',
  warning: 'bg-status-warning/10 text-status-warning',
  info: 'bg-status-info/10 text-status-info',
};

const channelTypeIcons: Record<string, React.ElementType> = {
  slack: Hash,
  email: Mail,
  pagerduty: AlertCircle,
  webhook: Webhook,
  msteams: MessageSquare,
};

export default function AlertingPage() {
  const [activeTab, setActiveTab] = useState<TabKey>('rules');
  const [showRuleModal, setShowRuleModal] = useState(false);
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null);
  const [showChannelModal, setShowChannelModal] = useState(false);
  const [showSilenceModal, setShowSilenceModal] = useState(false);

  const { data: rules, isLoading: rulesLoading } = useAlertRules();
  const { data: events, isLoading: eventsLoading } = useAlertEvents();
  const { data: channels, isLoading: channelsLoading } = useNotificationChannels();
  const { data: silences, isLoading: silencesLoading } = useAlertSilences();

  const acknowledgeAlert = useAcknowledgeAlert();
  const resolveAlert = useResolveAlert();
  const deleteRule = useDeleteAlertRule();
  const testChannel = useTestNotificationChannel();

  const ruleColumns: Column<AlertRule>[] = [
    {
      key: 'name',
      header: 'Rule',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          {row.description && (
            <p className="text-xs text-muted-foreground truncate max-w-[300px]">{row.description}</p>
          )}
        </div>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.type}
        </span>
      ),
    },
    {
      key: 'severity',
      header: 'Severity',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded capitalize font-medium', severityColors[row.severity])}>
          {row.severity}
        </span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName || 'All'}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
        />
      ),
    },
    {
      key: 'activeAlerts',
      header: 'Active',
      accessor: (row) => (
        <span className={cn('tabular-nums text-sm font-medium', row.activeAlerts > 0 ? 'text-status-error' : 'text-muted-foreground')}>
          {row.activeAlerts}
        </span>
      ),
      sortAccessor: (row) => row.activeAlerts,
      align: 'center',
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => { setEditingRule(row); setShowRuleModal(true); }}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Edit rule"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => { if (confirm('Delete this alert rule?')) deleteRule.mutate(row.id); }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete rule"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const eventColumns: Column<AlertEvent>[] = [
    {
      key: 'severity',
      header: 'Severity',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded capitalize font-medium', severityColors[row.severity])}>
          {row.severity}
        </span>
      ),
    },
    {
      key: 'rule',
      header: 'Rule',
      accessor: (row) => (
        <span className="font-medium text-foreground">{row.ruleName}</span>
      ),
    },
    {
      key: 'message',
      header: 'Message',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground truncate max-w-[300px] block">{row.message}</span>
      ),
      sortable: false,
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName || '--'}</span>
      ),
    },
    {
      key: 'firedAt',
      header: 'Fired',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.firedAt)}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          {row.status === 'firing' && (
            <>
              <button
                onClick={() => acknowledgeAlert.mutate(row.id)}
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
                title="Acknowledge"
              >
                <Check className="h-3 w-3" />
                Ack
              </button>
              <button
                onClick={() => resolveAlert.mutate(row.id)}
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-status-success hover:bg-status-success/10 transition-colors"
                title="Resolve"
              >
                <CheckCircle className="h-3 w-3" />
                Resolve
              </button>
            </>
          )}
          {row.status === 'acknowledged' && (
            <button
              onClick={() => resolveAlert.mutate(row.id)}
              className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-status-success hover:bg-status-success/10 transition-colors"
              title="Resolve"
            >
              <CheckCircle className="h-3 w-3" />
              Resolve
            </button>
          )}
        </div>
      ),
      sortable: false,
    },
  ];

  const channelColumns: Column<NotificationChannel>[] = [
    {
      key: 'name',
      header: 'Channel',
      accessor: (row) => {
        const TypeIcon = channelTypeIcons[row.type] || Bell;
        return (
          <div className="flex items-center gap-2">
            <TypeIcon className="h-4 w-4 text-muted-foreground" />
            <span className="font-medium text-foreground">{row.name}</span>
          </div>
        );
      },
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.type === 'msteams' ? 'MS Teams' : row.type === 'pagerduty' ? 'PagerDuty' : row.type}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
        />
      ),
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => testChannel.mutate(row.id)}
            disabled={testChannel.isPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Test Channel"
          >
            <Send className="h-3 w-3" />
            Test
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const silenceColumns: Column<AlertSilence>[] = [
    {
      key: 'reason',
      header: 'Reason',
      accessor: (row) => <span className="font-medium text-foreground">{row.reason}</span>,
    },
    {
      key: 'duration',
      header: 'Duration',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.duration}</span>,
    },
    {
      key: 'matchers',
      header: 'Matchers',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {Object.entries(row.matchers).map(([k, v]) => (
            <span key={k} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
              {k}={v}
            </span>
          ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'creator',
      header: 'Creator',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.createdBy}</span>,
    },
    {
      key: 'endsAt',
      header: 'Expires',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.endsAt)}</span>,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Alerting</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Alert rules, notifications, and silence management
          </p>
        </div>
        <div className="flex items-center gap-2">
          {activeTab === 'rules' && (
            <button
              onClick={() => { setEditingRule(null); setShowRuleModal(true); }}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Rule
            </button>
          )}
          {activeTab === 'channels' && (
            <button
              onClick={() => setShowChannelModal(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Add Channel
            </button>
          )}
          {activeTab === 'silences' && (
            <button
              onClick={() => setShowSilenceModal(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Silence
            </button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-6">
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
        {activeTab === 'rules' && (
          <DataTable
            data={rules || []}
            columns={ruleColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search alert rules..."
            loading={rulesLoading}
            emptyMessage="No alert rules configured"
          />
        )}

        {activeTab === 'active' && (
          <DataTable
            data={events || []}
            columns={eventColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search active alerts..."
            loading={eventsLoading}
            emptyMessage="No active alerts"
          />
        )}

        {activeTab === 'channels' && (
          <DataTable
            data={channels || []}
            columns={channelColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search notification channels..."
            loading={channelsLoading}
            emptyMessage="No notification channels configured"
          />
        )}

        {activeTab === 'silences' && (
          <DataTable
            data={silences || []}
            columns={silenceColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search silences..."
            loading={silencesLoading}
            emptyMessage="No active silences"
          />
        )}
      </div>

      {/* Alert Rule Modal */}
      {showRuleModal && (
        <AlertRuleModal
          rule={editingRule}
          onClose={() => { setShowRuleModal(false); setEditingRule(null); }}
        />
      )}

      {/* Channel Modal */}
      {showChannelModal && (
        <NotificationChannelModal onClose={() => setShowChannelModal(false)} />
      )}

      {/* Silence Modal */}
      {showSilenceModal && (
        <SilenceModal onClose={() => setShowSilenceModal(false)} />
      )}
    </div>
  );
}

// ============================================================
// Alert Rule Modal
// ============================================================

function AlertRuleModal({ rule, onClose }: { rule: AlertRule | null; onClose: () => void }) {
  const createRule = useCreateAlertRule();
  const updateRule = useUpdateAlertRule();
  const [form, setForm] = useState({
    name: rule?.name || '',
    description: rule?.description || '',
    type: rule?.type || 'threshold' as AlertRule['type'],
    severity: rule?.severity || 'warning' as AlertSeverity,
    query: rule?.query || '',
    threshold: rule?.threshold?.toString() || '',
    duration: rule?.duration || '5m',
    enabled: rule?.enabled ?? true,
  });

  const handleSave = async () => {
    const data: Partial<AlertRule> = {
      name: form.name,
      description: form.description || undefined,
      type: form.type,
      severity: form.severity,
      query: form.query,
      threshold: form.threshold ? parseFloat(form.threshold) : undefined,
      duration: form.duration,
      enabled: form.enabled,
    };

    try {
      if (rule) {
        await updateRule.mutateAsync({ id: rule.id, data });
      } else {
        await createRule.mutateAsync(data);
      }
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  const isPending = createRule.isPending || updateRule.isPending;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">
            {rule ? 'Edit Alert Rule' : 'Create Alert Rule'}
          </h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="High CPU Usage"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Triggers when CPU exceeds threshold"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Type</label>
              <select
                value={form.type}
                onChange={(e) => setForm((f) => ({ ...f, type: e.target.value as AlertRule['type'] }))}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="threshold">Threshold</option>
                <option value="anomaly">Anomaly</option>
                <option value="absence">Absence</option>
                <option value="change">Change</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Severity</label>
              <div className="flex gap-1.5">
                {(['critical', 'warning', 'info'] as const).map((sev) => (
                  <button
                    key={sev}
                    onClick={() => setForm((f) => ({ ...f, severity: sev }))}
                    className={cn(
                      'flex-1 px-2 py-1.5 rounded-md text-xs font-medium transition-colors capitalize',
                      form.severity === sev
                        ? severityColors[sev]
                        : 'bg-muted text-muted-foreground hover:text-foreground'
                    )}
                  >
                    {sev}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">PromQL Query</label>
            <textarea
              value={form.query}
              onChange={(e) => setForm((f) => ({ ...f, query: e.target.value }))}
              placeholder='avg(rate(cpu_usage_seconds_total[5m])) > 0.8'
              rows={3}
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-none"
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Threshold</label>
              <input
                type="number"
                value={form.threshold}
                onChange={(e) => setForm((f) => ({ ...f, threshold: e.target.value }))}
                placeholder="0.8"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Duration</label>
              <input
                type="text"
                value={form.duration}
                onChange={(e) => setForm((f) => ({ ...f, duration: e.target.value }))}
                placeholder="5m"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
              className="rounded border-border text-primary focus:ring-ring"
            />
            <span className="text-sm text-foreground">Enabled</span>
          </label>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={isPending || !form.name}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {rule ? 'Update Rule' : 'Create Rule'}
          </button>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// Notification Channel Modal
// ============================================================

const channelTypeFields: Record<NotificationChannelType, { label: string; fields: { key: string; label: string; type: string; placeholder: string }[] }> = {
  slack: {
    label: 'Slack',
    fields: [
      { key: 'webhookUrl', label: 'Webhook URL', type: 'text', placeholder: 'https://hooks.slack.com/services/...' },
      { key: 'channel', label: 'Channel', type: 'text', placeholder: '#alerts' },
    ],
  },
  email: {
    label: 'Email',
    fields: [
      { key: 'recipients', label: 'Recipients', type: 'text', placeholder: 'team@example.com, ops@example.com' },
      { key: 'smtpHost', label: 'SMTP Host', type: 'text', placeholder: 'smtp.example.com' },
      { key: 'smtpPort', label: 'SMTP Port', type: 'text', placeholder: '587' },
    ],
  },
  pagerduty: {
    label: 'PagerDuty',
    fields: [
      { key: 'integrationKey', label: 'Integration Key', type: 'password', placeholder: 'Integration key' },
      { key: 'severity', label: 'Default Severity', type: 'text', placeholder: 'critical' },
    ],
  },
  webhook: {
    label: 'Webhook',
    fields: [
      { key: 'url', label: 'URL', type: 'text', placeholder: 'https://example.com/webhook' },
      { key: 'method', label: 'Method', type: 'text', placeholder: 'POST' },
      { key: 'headers', label: 'Headers (JSON)', type: 'text', placeholder: '{"Authorization": "Bearer ..."}' },
    ],
  },
  msteams: {
    label: 'MS Teams',
    fields: [
      { key: 'webhookUrl', label: 'Webhook URL', type: 'text', placeholder: 'https://outlook.office.com/webhook/...' },
    ],
  },
};

function NotificationChannelModal({ onClose }: { onClose: () => void }) {
  const createChannel = useCreateNotificationChannel();
  const [form, setForm] = useState({
    name: '',
    type: 'slack' as NotificationChannelType,
    enabled: true,
    config: {} as Record<string, string>,
  });

  const typeConfig = channelTypeFields[form.type];

  const handleSave = async () => {
    try {
      await createChannel.mutateAsync({
        name: form.name,
        type: form.type,
        enabled: form.enabled,
        config: form.config,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Add Notification Channel</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="Production Alerts"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Type</label>
            <div className="flex flex-wrap gap-1.5">
              {(Object.keys(channelTypeFields) as NotificationChannelType[]).map((type) => (
                <button
                  key={type}
                  onClick={() => setForm((f) => ({ ...f, type, config: {} }))}
                  className={cn(
                    'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                    form.type === type
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {channelTypeFields[type].label}
                </button>
              ))}
            </div>
          </div>

          {/* Type-specific fields */}
          {typeConfig.fields.map((field) => (
            <div key={field.key} className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">{field.label}</label>
              <input
                type={field.type}
                value={form.config[field.key] || ''}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    config: { ...f.config, [field.key]: e.target.value },
                  }))
                }
                placeholder={field.placeholder}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          ))}

          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
              className="rounded border-border text-primary focus:ring-ring"
            />
            <span className="text-sm text-foreground">Enabled</span>
          </label>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createChannel.isPending || !form.name}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createChannel.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Add Channel
          </button>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// Silence Modal
// ============================================================

function SilenceModal({ onClose }: { onClose: () => void }) {
  const createSilence = useCreateAlertSilence();
  const [form, setForm] = useState({
    reason: '',
    duration: '1h',
    matcherKey: '',
    matcherValue: '',
    matchers: {} as Record<string, string>,
  });

  const addMatcher = () => {
    if (form.matcherKey && form.matcherValue) {
      setForm((f) => ({
        ...f,
        matchers: { ...f.matchers, [f.matcherKey]: f.matcherValue },
        matcherKey: '',
        matcherValue: '',
      }));
    }
  };

  const removeMatcher = (key: string) => {
    setForm((f) => {
      const m = { ...f.matchers };
      delete m[key];
      return { ...f, matchers: m };
    });
  };

  const handleSave = async () => {
    try {
      await createSilence.mutateAsync({
        reason: form.reason,
        duration: form.duration,
        matchers: form.matchers,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create Silence</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Reason</label>
            <input
              type="text"
              value={form.reason}
              onChange={(e) => setForm((f) => ({ ...f, reason: e.target.value }))}
              placeholder="Scheduled maintenance window"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Duration</label>
            <select
              value={form.duration}
              onChange={(e) => setForm((f) => ({ ...f, duration: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="30m">30 minutes</option>
              <option value="1h">1 hour</option>
              <option value="2h">2 hours</option>
              <option value="4h">4 hours</option>
              <option value="8h">8 hours</option>
              <option value="24h">24 hours</option>
              <option value="7d">7 days</option>
            </select>
          </div>

          <div className="space-y-2">
            <label className="text-sm font-medium text-foreground">Matchers</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={form.matcherKey}
                onChange={(e) => setForm((f) => ({ ...f, matcherKey: e.target.value }))}
                placeholder="Label name"
                className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-xs font-mono
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <input
                type="text"
                value={form.matcherValue}
                onChange={(e) => setForm((f) => ({ ...f, matcherValue: e.target.value }))}
                placeholder="Value"
                className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-xs font-mono
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <button
                onClick={addMatcher}
                disabled={!form.matcherKey || !form.matcherValue}
                className="h-8 px-2.5 rounded border border-border text-xs text-muted-foreground
                  hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
              >
                <Plus className="h-3.5 w-3.5" />
              </button>
            </div>
            {Object.entries(form.matchers).length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(form.matchers).map(([k, v]) => (
                  <span
                    key={k}
                    className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
                  >
                    {k}={v}
                    <button onClick={() => removeMatcher(k)} className="hover:text-foreground">
                      <X className="h-3 w-3" />
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createSilence.isPending || !form.reason}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createSilence.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create Silence
          </button>
        </div>
      </div>
    </div>
  );
}
