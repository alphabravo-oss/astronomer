import { Loader2, Pencil } from 'lucide-react';
import { useGeneralSettings } from '@/lib/hooks';
import { ActionButton } from '@/components/ui/action-button';
import { SettingRow } from './-shared';

export function GeneralTab({ onEdit }: { onEdit: () => void }) {
  const { data: generalSettings, isLoading: generalLoading } = useGeneralSettings();

  return (
    <div className="max-w-2xl space-y-6">
      {generalLoading ? (
        <div className="flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : (
        <div className="rounded-xl border border-border bg-card p-6 space-y-4">
          <div className="flex items-start justify-between gap-4">
            <div>
              <h3 className="text-base font-semibold text-foreground">Platform Settings</h3>
              <p className="text-xs text-muted-foreground mt-0.5">Name, heartbeat, session, audit + metrics.</p>
            </div>
            <ActionButton icon={<Pencil className="h-3.5 w-3.5" />} onClick={onEdit}>
              Edit
            </ActionButton>
          </div>
          <div className="divide-y divide-border/60">
            <SettingRow label="Platform name" value={generalSettings?.platformName ?? 'Astronomer'} />
            <SettingRow label="Agent heartbeat" value={`${generalSettings?.agentHeartbeatInterval ?? 30}s`} />
            <SettingRow label="Session timeout" value={`${generalSettings?.defaultSessionTimeout ?? 60} min`} />
            <SettingRow
              label="Audit logging"
              value={(generalSettings?.enableAuditLogging ?? true) ? 'Enabled' : 'Disabled'}
            />
            <SettingRow
              label="Metrics collection"
              value={(generalSettings?.metricsCollection ?? true) ? 'Enabled' : 'Disabled'}
            />
          </div>
        </div>
      )}
    </div>
  );
}
