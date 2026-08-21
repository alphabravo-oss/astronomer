import { useAppForm, useStore } from '@/lib/form';
import { useCreateLoggingOutput, useClusters } from '@/lib/hooks';
import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import type { LoggingOutputType } from '@/types';
import { toastError } from '@/lib/toast';

const outputTypeFields: Record<
  LoggingOutputType,
  { label: string; fields: { key: string; label: string; type: string; placeholder: string }[] }
> = {
  elasticsearch: {
    label: 'Elasticsearch',
    fields: [
      { key: 'url', label: 'URL', type: 'text', placeholder: 'https://elasticsearch.example.com:9200' },
      { key: 'index', label: 'Index', type: 'text', placeholder: 'kubernetes-logs' },
      { key: 'username', label: 'Username', type: 'text', placeholder: 'elastic' },
      { key: 'password', label: 'Password', type: 'password', placeholder: 'Password' },
    ],
  },
  loki: {
    label: 'Loki',
    fields: [
      { key: 'url', label: 'URL', type: 'text', placeholder: 'https://loki.example.com:3100' },
      { key: 'tenant_id', label: 'Tenant ID', type: 'text', placeholder: 'default' },
      { key: 'labels', label: 'Labels', type: 'text', placeholder: 'job=kubernetes, env=production' },
    ],
  },
  splunk: {
    label: 'Splunk',
    fields: [
      { key: 'hec_url', label: 'HEC URL', type: 'text', placeholder: 'https://splunk.example.com:8088' },
      { key: 'token', label: 'HEC Token', type: 'password', placeholder: 'Token' },
      { key: 'index', label: 'Index', type: 'text', placeholder: 'main' },
      { key: 'source', label: 'Source', type: 'text', placeholder: 'kubernetes' },
    ],
  },
  cloudwatch: {
    label: 'CloudWatch',
    fields: [
      { key: 'region', label: 'Region', type: 'text', placeholder: 'us-east-1' },
      { key: 'log_group', label: 'Log Group', type: 'text', placeholder: '/kubernetes/cluster-logs' },
      { key: 'access_key', label: 'Access Key', type: 'text', placeholder: 'AKIA...' },
      { key: 'secret_key', label: 'Secret Key', type: 'password', placeholder: 'Secret key' },
    ],
  },
  datadog: {
    label: 'Datadog',
    fields: [
      { key: 'api_key', label: 'API Key', type: 'password', placeholder: 'Datadog API key' },
      { key: 'site', label: 'Site', type: 'text', placeholder: 'datadoghq.com' },
      { key: 'service', label: 'Service', type: 'text', placeholder: 'kubernetes' },
      { key: 'source', label: 'Source', type: 'text', placeholder: 'kubernetes' },
    ],
  },
  s3: {
    label: 'S3',
    fields: [
      { key: 'bucket', label: 'Bucket', type: 'text', placeholder: 'my-log-bucket' },
      { key: 'region', label: 'Region', type: 'text', placeholder: 'us-east-1' },
      { key: 'prefix', label: 'Prefix', type: 'text', placeholder: 'logs/' },
      { key: 'access_key', label: 'Access Key', type: 'text', placeholder: 'AKIA...' },
      { key: 'secret_key', label: 'Secret Key', type: 'password', placeholder: 'Secret key' },
    ],
  },
  syslog: {
    label: 'Syslog',
    fields: [
      { key: 'host', label: 'Host', type: 'text', placeholder: 'syslog.example.com' },
      { key: 'port', label: 'Port', type: 'text', placeholder: '514' },
      { key: 'protocol', label: 'Protocol', type: 'text', placeholder: 'tcp' },
      { key: 'facility', label: 'Facility', type: 'text', placeholder: 'local0' },
    ],
  },
};

export function CreateOutputModal({ onClose }: { onClose: () => void }) {
  const createOutput = useCreateLoggingOutput();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];

  const form = useAppForm({
    defaultValues: {
      name: '',
      type: 'elasticsearch' as LoggingOutputType,
      clusterId: '',
      enabled: true,
      config: {} as Record<string, string>,
    },
    validators: {
      // Old pre-submit check, ported 1:1.
      onSubmit: ({ value }) => (!value.name ? 'Name is required' : undefined),
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        await createOutput.mutateAsync({
          name: value.name,
          type: value.type,
          clusterId: value.clusterId || undefined,
          enabled: value.enabled,
          config: value.config,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const outputName = useStore(form.store, (s) => s.values.name);
  const outputType = useStore(form.store, (s) => s.values.type);
  const outputConfig = useStore(form.store, (s) => s.values.config);

  const typeConfig = outputTypeFields[outputType];

  return (
    <ModalShell
      title="Create Logging Output"
      onClose={onClose}
      size="md"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={!outputName}
            loading={createOutput.isPending}
          >
            Create Output
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-1.5">
        <label htmlFor="logging-output-name" className="text-sm font-medium text-foreground">Name</label>
        <form.Field name="name">
          {(field) => (
            <Input
              id="logging-output-name"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Production Elasticsearch"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <p className="text-sm font-medium text-foreground">Type</p>
        <div className="flex flex-wrap gap-1.5">
          {(Object.keys(outputTypeFields) as LoggingOutputType[]).map((type) => (
            <button
              key={type}
              type="button"
              onClick={() => {
                form.setFieldValue('type', type);
                form.setFieldValue('config', {});
              }}
              className={cn(
                'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                outputType === type
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:text-foreground'
              )}
            >
              {outputTypeFields[type].label}
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-1.5">
        <label htmlFor="logging-output-cluster" className="text-sm font-medium text-foreground">Cluster (optional)</label>
        <form.Field name="clusterId">
          {(field) => (
            <Select
              id="logging-output-cluster"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            >
              <option value="">All Clusters</option>
              {clusters.map((cluster) => (
                <option key={cluster.id} value={cluster.id}>
                  {cluster.displayName}
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      {typeConfig.fields.map((field) => {
        const fieldId = `logging-output-${field.key}`;
        return (
          <div key={field.key} className="space-y-1.5">
            <label htmlFor={fieldId} className="text-sm font-medium text-foreground">{field.label}</label>
            <Input
              id={fieldId}
              type={field.type}
              value={outputConfig[field.key] || ''}
              onChange={(e) =>
                form.setFieldValue('config', { ...outputConfig, [field.key]: e.target.value })
              }
              placeholder={field.placeholder}
            />
          </div>
        );
      })}

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
