import { useAppForm, useStore } from '@/lib/form';
import { useCreateAlertRule, useUpdateAlertRule } from '@/lib/hooks';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { cn, statusBgColor } from '@/lib/utils';
import type { AlertRule, AlertSeverity } from '@/types';

export function AlertRuleModal({
  rule,
  onClose,
  clusterId,
}: {
  rule: AlertRule | null;
  onClose: () => void;
  clusterId?: string;
}) {
  const createRule = useCreateAlertRule();
  const updateRule = useUpdateAlertRule();
  const form = useAppForm({
    defaultValues: {
      name: rule?.name || '',
      description: rule?.description || '',
      type: rule?.type || 'threshold' as AlertRule['type'],
      severity: rule?.severity || 'warning' as AlertSeverity,
      query: rule?.query || '',
      threshold: rule?.threshold?.toString() || '',
      duration: rule?.duration || '5m',
      enabled: rule?.enabled ?? true,
      // Sprint 072 — anomaly knobs. The anomaly evaluator is driven off the
      // single `type` field (type === 'anomaly'); there is no separate rule-kind
      // toggle, so the Type select and the payload can never disagree.
      metric: rule?.metric || 'cluster_cpu_percent',
      anomalyStddev: rule?.anomalyStddev?.toString() || '3',
      anomalyWindowSeconds: rule?.anomalyWindowSeconds?.toString() || '86400',
      anomalyMinSamples: rule?.anomalyMinSamples?.toString() || '50',
      anomalyDirection: (rule?.anomalyDirection || 'above') as 'above' | 'below' | 'either',
    },
    onSubmit: async ({ value }) => {
      const isAnomaly = value.type === 'anomaly';
      const data: Partial<AlertRule> & {
        rule_kind?: string;
        cluster_id?: string;
        anomaly_stddev?: number;
        anomaly_window_seconds?: number;
        anomaly_min_samples?: number;
        anomaly_direction?: string;
      } = {
        name: value.name,
        description: value.description || undefined,
        type: value.type,
        severity: value.severity,
        query: value.query,
        threshold: value.threshold ? parseFloat(value.threshold) : undefined,
        duration: value.duration,
        enabled: value.enabled,
        // Send the new fields with the snake_case names the backend
        // CreateAlertRuleRequest expects. The handler also reads camelCase
        // via Type/RuleType aliases, but the snake_case path is
        // canonical. rule_kind is derived from `type` so the two stay in sync.
        rule_kind: isAnomaly ? 'anomaly' : 'threshold',
        cluster_id: clusterId || rule?.clusterId,
        metric: isAnomaly ? value.metric : undefined,
        anomaly_stddev: isAnomaly ? parseFloat(value.anomalyStddev) : undefined,
        anomaly_window_seconds: isAnomaly ? parseInt(value.anomalyWindowSeconds, 10) : undefined,
        anomaly_min_samples: isAnomaly ? parseInt(value.anomalyMinSamples, 10) : undefined,
        anomaly_direction: isAnomaly ? value.anomalyDirection : undefined,
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
    },
  });

  const ruleName = useStore(form.store, (s) => s.values.name);
  const ruleType = useStore(form.store, (s) => s.values.type);
  const severity = useStore(form.store, (s) => s.values.severity);

  const isPending = createRule.isPending || updateRule.isPending;

  return (
    <ModalShell
      title={rule ? 'Edit Alert Rule' : 'Create Alert Rule'}
      onClose={onClose}
      size="md"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={!ruleName}
            loading={isPending}
          >
            {rule ? 'Update Rule' : 'Create Rule'}
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
              placeholder="High CPU Usage"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Description</label>
        <form.Field name="description">
          {(field) => (
            <Input
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Triggers when CPU exceeds threshold"
            />
          )}
        </form.Field>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Type</label>
          <form.Field name="type">
            {(field) => (
              <Select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value as AlertRule['type'])}
                onBlur={field.handleBlur}
              >
                <option value="threshold">Threshold</option>
                <option value="anomaly">Anomaly</option>
                <option value="absence">Absence</option>
                <option value="change">Change</option>
              </Select>
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Severity</label>
          <div className="flex gap-1.5">
            {(['critical', 'warning', 'info'] as const).map((sev) => (
              <button
                key={sev}
                type="button"
                onClick={() => form.setFieldValue('severity', sev)}
                className={cn(
                  'flex-1 px-2 py-1.5 rounded-md text-xs font-medium transition-colors capitalize',
                  severity === sev
                    ? statusBgColor(sev)
                    : 'bg-muted text-muted-foreground hover:text-foreground',
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
        <form.Field name="query">
          {(field) => (
            <Textarea
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="avg(rate(cpu_usage_seconds_total[5m])) > 0.8"
              rows={3}
              className="resize-none text-sm"
            />
          )}
        </form.Field>
      </div>

      {ruleType === 'anomaly' && (
        <div className="space-y-3 p-3 rounded-md border border-border bg-muted/30">
          <div className="text-xs text-muted-foreground">
            Anomaly rules fire when the current value of <b>metric</b> deviates from the
            rolling baseline by more than <b>stddev</b> standard deviations in the chosen
            <b> direction</b>. Newly-created rules short-circuit to no-fire until
            <b> min samples</b> datapoints accumulate.
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Metric</label>
            <form.Field name="metric">
              {(field) => (
                <Select
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                >
                  <option value="cluster_cpu_percent">cluster_cpu_percent</option>
                  <option value="cluster_memory_percent">cluster_memory_percent</option>
                  <option value="pod_count">pod_count</option>
                  <option value="node_count">node_count</option>
                  <option value="pod_restart_rate">pod_restart_rate</option>
                </Select>
              )}
            </form.Field>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Stddev (σ)</label>
              <form.Field name="anomalyStddev">
                {(field) => (
                  <Input
                    type="number"
                    step="0.1"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    placeholder="3"
                  />
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Window</label>
              <form.Field name="anomalyWindowSeconds">
                {(field) => (
                  <Select
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  >
                    <option value="3600">1h</option>
                    <option value="21600">6h</option>
                    <option value="86400">24h</option>
                    <option value="604800">7d</option>
                  </Select>
                )}
              </form.Field>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Direction</label>
              <form.Field name="anomalyDirection">
                {(field) => (
                  <Select
                    value={field.state.value}
                    onChange={(e) =>
                      field.handleChange(e.target.value as 'above' | 'below' | 'either')
                    }
                    onBlur={field.handleBlur}
                  >
                    <option value="above">Above baseline</option>
                    <option value="below">Below baseline</option>
                    <option value="either">Either direction</option>
                  </Select>
                )}
              </form.Field>
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Min samples</label>
              <form.Field name="anomalyMinSamples">
                {(field) => (
                  <Input
                    type="number"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                    placeholder="50"
                  />
                )}
              </form.Field>
            </div>
          </div>
        </div>
      )}

      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Threshold</label>
          <form.Field name="threshold">
            {(field) => (
              <Input
                type="number"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="0.8"
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Duration</label>
          <form.Field name="duration">
            {(field) => (
              <Input
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="5m"
              />
            )}
          </form.Field>
        </div>
      </div>

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
