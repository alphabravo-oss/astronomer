import { useEffect } from 'react';
import { useGeneralSettings, useSaveGeneralSettings } from '@/lib/hooks';
import { useAppForm } from '@/lib/form';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

export function GeneralEditModal({ onClose }: { onClose: () => void }) {
  const { data: generalSettings } = useGeneralSettings();
  const saveGeneralSettings = useSaveGeneralSettings();

  const generalForm = useAppForm({
    defaultValues: {
      platformName: 'Astronomer',
      agentHeartbeatInterval: 30,
      defaultSessionTimeout: 60,
      enableAuditLogging: true,
      metricsCollection: true,
    },
    onSubmit: async ({ value }) => {
      try {
        await saveGeneralSettings.mutateAsync(value);
        onClose();
      } catch {
        // Error is handled by the mutation's onError callback
      }
    },
  });

  // Rebase the form whenever the settings snapshot lands (initial load and
  // post-save invalidation), exactly like the old setGeneralForm effect.
  useEffect(() => {
    if (generalSettings) {
      generalForm.reset({
        platformName: generalSettings.platformName ?? 'Astronomer',
        agentHeartbeatInterval: generalSettings.agentHeartbeatInterval ?? 30,
        defaultSessionTimeout: generalSettings.defaultSessionTimeout ?? 60,
        enableAuditLogging: generalSettings.enableAuditLogging ?? true,
        metricsCollection: generalSettings.metricsCollection ?? true,
      });
    }
  }, [generalForm, generalSettings]);

  return (
    <ModalShell
      title="Platform Settings"
      onClose={onClose}
      size="md"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void generalForm.handleSubmit()}
            loading={saveGeneralSettings.isPending}
          >
            Save Settings
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <label htmlFor="platform-name" className="text-sm font-medium text-foreground">Platform Name</label>
          <generalForm.Field name="platformName">
            {(field) => (
              <Input
                id="platform-name"
                aria-label="Platform Name"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              />
            )}
          </generalForm.Field>
        </div>

        <div className="space-y-1.5">
          <label htmlFor="agent-heartbeat" className="text-sm font-medium text-foreground">Agent Heartbeat Interval</label>
          <generalForm.Field name="agentHeartbeatInterval">
            {(field) => (
              <Select
                id="agent-heartbeat"
                aria-label="Agent Heartbeat Interval"
                value={field.state.value}
                onChange={(e) => field.handleChange(Number(e.target.value))}
                onBlur={field.handleBlur}
              >
                <option value={15}>15 seconds</option>
                <option value={30}>30 seconds</option>
                <option value={60}>60 seconds</option>
              </Select>
            )}
          </generalForm.Field>
        </div>

        <div className="space-y-1.5">
          <label htmlFor="session-timeout" className="text-sm font-medium text-foreground">Default Session Timeout</label>
          <generalForm.Field name="defaultSessionTimeout">
            {(field) => (
              <Select
                id="session-timeout"
                aria-label="Default Session Timeout"
                value={field.state.value}
                onChange={(e) => field.handleChange(Number(e.target.value))}
                onBlur={field.handleBlur}
              >
                <option value={30}>30 minutes</option>
                <option value={60}>1 hour</option>
                <option value={480}>8 hours</option>
                <option value={1440}>24 hours</option>
              </Select>
            )}
          </generalForm.Field>
        </div>

        <div className="flex items-center justify-between p-4 rounded-lg border border-border">
          <div>
            <p className="text-sm font-medium text-foreground">Enable Audit Logging</p>
            <p className="text-xs text-muted-foreground">Log all API actions for compliance</p>
          </div>
          <generalForm.Field name="enableAuditLogging">
            {(field) => (
              <Switch
                aria-label="Enable Audit Logging"
                checked={field.state.value}
                onCheckedChange={field.handleChange}
                onBlur={field.handleBlur}
              />
            )}
          </generalForm.Field>
        </div>

        <div className="flex items-center justify-between p-4 rounded-lg border border-border">
          <div>
            <p className="text-sm font-medium text-foreground">Metrics Collection</p>
            <p className="text-xs text-muted-foreground">Collect and aggregate cluster metrics</p>
          </div>
          <generalForm.Field name="metricsCollection">
            {(field) => (
              <Switch
                aria-label="Metrics Collection"
                checked={field.state.value}
                onCheckedChange={field.handleChange}
                onBlur={field.handleBlur}
              />
            )}
          </generalForm.Field>
        </div>
      </div>
    </ModalShell>
  );
}
