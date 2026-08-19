import { useAppForm, useStore } from '@/lib/form';
import { useCreateNotificationChannel } from '@/lib/hooks';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { cn } from '@/lib/utils';
import type { NotificationChannelType } from '@/types';

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

export function NotificationChannelModal({ onClose }: { onClose: () => void }) {
  const createChannel = useCreateNotificationChannel();
  const form = useAppForm({
    defaultValues: {
      name: '',
      type: 'slack' as NotificationChannelType,
      enabled: true,
      config: {} as Record<string, string>,
    },
    onSubmit: async ({ value }) => {
      try {
        await createChannel.mutateAsync({
          name: value.name,
          type: value.type,
          enabled: value.enabled,
          config: value.config,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const channelName = useStore(form.store, (s) => s.values.name);
  const channelType = useStore(form.store, (s) => s.values.type);
  const config = useStore(form.store, (s) => s.values.config);

  const typeConfig = channelTypeFields[channelType];

  return (
    <ModalShell
      title="Add Notification Channel"
      onClose={onClose}
      size="md"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={!channelName}
            loading={createChannel.isPending}
          >
            Add Channel
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Name</label>
        <form.Field name="name">
          {(field) => (
            <Input
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Production Alerts"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Type</label>
        <div className="flex flex-wrap gap-1.5">
          {(Object.keys(channelTypeFields) as NotificationChannelType[]).map((type) => (
            <button
              key={type}
              type="button"
              onClick={() => {
                form.setFieldValue('type', type);
                form.setFieldValue('config', {});
              }}
              className={cn(
                'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                channelType === type
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:text-foreground',
              )}
            >
              {channelTypeFields[type].label}
            </button>
          ))}
        </div>
      </div>

      {typeConfig.fields.map((field) => (
        <div key={field.key} className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">{field.label}</label>
          <Input
            type={field.type}
            value={config[field.key] || ''}
            onChange={(e) =>
              form.setFieldValue('config', { ...config, [field.key]: e.target.value })
            }
            placeholder={field.placeholder}
          />
        </div>
      ))}

      <label className="flex items-center gap-2 cursor-pointer">
        <form.Field name="enabled">
          {(field) => (
            <input
              type="checkbox"
              checked={field.state.value}
              onChange={(e) => field.handleChange(e.target.checked)}
              onBlur={field.handleBlur}
              className="rounded border-border text-primary focus:ring-ring"
            />
          )}
        </form.Field>
        <span className="text-sm text-foreground">Enabled</span>
      </label>
    </ModalShell>
  );
}
