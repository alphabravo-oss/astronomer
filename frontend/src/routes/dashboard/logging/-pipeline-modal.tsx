import { useAppForm, useStore } from '@/lib/form';
import { useCreateLoggingPipeline, useClusters, useClusterNamespaces, useLoggingOutputs } from '@/lib/hooks';
import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';
import { Plus, X } from 'lucide-react';
import { toastError } from '@/lib/toast';

export function CreatePipelineModal({
  onClose,
  clusterId,
}: {
  onClose: () => void;
  clusterId?: string;
}) {
  const createPipeline = useCreateLoggingPipeline();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];
  const { data: outputs, isLoading: outputsLoading } = useLoggingOutputs();
  const outputList = outputs || [];

  const pipelineForm = useAppForm({
    defaultValues: {
      name: '',
      description: '',
      clusterId: clusterId || '',
      namespaces: [] as string[],
      outputIds: [] as string[],
      labelKey: '',
      labelValue: '',
      labels: {} as Record<string, string>,
      enabled: true,
    },
    validators: {
      // Old pre-submit checks, ported 1:1 (same messages, same order).
      onSubmit: ({ value }) =>
        !value.name
          ? 'Name is required'
          : !value.clusterId
            ? 'Select a cluster'
            : value.outputIds.length === 0
              ? 'Select at least one output'
              : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      const filters = Object.entries(value.labels).map(([field, pattern]) => ({
        type: 'include' as const,
        field,
        pattern,
      }));

      try {
        await createPipeline.mutateAsync({
          name: value.name,
          description: value.description || undefined,
          clusterId: value.clusterId || undefined,
          namespaces: value.namespaces,
          outputIds: value.outputIds,
          filters,
          enabled: value.enabled,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  // Chips / KV rows render off the whole value object — same re-render
  // behavior as the previous useState form.
  const form = useStore(pipelineForm.store, (s) => s.values);

  const { data: namespacesData } = useClusterNamespaces(form.clusterId);
  const namespaces = namespacesData || [];

  const toggleNamespace = (ns: string) => {
    pipelineForm.setFieldValue(
      'namespaces',
      form.namespaces.includes(ns)
        ? form.namespaces.filter((n) => n !== ns)
        : [...form.namespaces, ns],
    );
  };

  const toggleOutput = (id: string) => {
    pipelineForm.setFieldValue(
      'outputIds',
      form.outputIds.includes(id)
        ? form.outputIds.filter((o) => o !== id)
        : [...form.outputIds, id],
    );
  };

  const addLabel = () => {
    if (form.labelKey && form.labelValue) {
      pipelineForm.setFieldValue('labels', { ...form.labels, [form.labelKey]: form.labelValue });
      pipelineForm.setFieldValue('labelKey', '');
      pipelineForm.setFieldValue('labelValue', '');
    }
  };

  const removeLabel = (key: string) => {
    const labels = { ...form.labels };
    delete labels[key];
    pipelineForm.setFieldValue('labels', labels);
  };

  return (
    <ModalShell
      title="Create Logging Pipeline"
      onClose={onClose}
      size="lg"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void pipelineForm.handleSubmit()}
            disabled={!form.name || !form.clusterId || form.outputIds.length === 0}
            loading={createPipeline.isPending}
          >
            Create Pipeline
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label htmlFor="logging-pipeline-name" className="text-sm font-medium text-foreground">Name</label>
          <pipelineForm.Field name="name">
            {(field) => (
              <Input
                id="logging-pipeline-name"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Production Log Pipeline"
              />
            )}
          </pipelineForm.Field>
        </div>
        {!clusterId && (
        <div className="space-y-1.5">
          <label htmlFor="logging-pipeline-cluster" className="text-sm font-medium text-foreground">Cluster</label>
          <pipelineForm.Field name="clusterId">
            {(field) => (
              <Select
                id="logging-pipeline-cluster"
                value={field.state.value}
                onChange={(e) => {
                  field.handleChange(e.target.value);
                  pipelineForm.setFieldValue('namespaces', []);
                }}
                onBlur={field.handleBlur}
              >
                <option value="">Select a cluster</option>
                {clusters.map((cluster) => (
                  <option key={cluster.id} value={cluster.id}>
                    {cluster.displayName}
                  </option>
                ))}
              </Select>
            )}
          </pipelineForm.Field>
        </div>
        )}
      </div>

      <div className="space-y-1.5">
        <label htmlFor="logging-pipeline-description" className="text-sm font-medium text-foreground">Description</label>
        <pipelineForm.Field name="description">
          {(field) => (
            <Textarea
              id="logging-pipeline-description"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Describe this pipeline's purpose"
              className="min-h-[80px]"
            />
          )}
        </pipelineForm.Field>
      </div>

      {form.clusterId && (
        <div className="space-y-1.5">
          <p className="text-sm font-medium text-foreground">Namespaces</p>
          <div className="flex flex-wrap gap-1.5 max-h-32 overflow-y-auto p-2 rounded-md border border-border bg-background">
            {namespaces.length === 0 ? (
              <span className="text-xs text-muted-foreground">No namespaces found</span>
            ) : (
              namespaces.map((ns) => (
                <button
                  key={ns.name}
                  type="button"
                  onClick={() => toggleNamespace(ns.name)}
                  className={cn(
                    'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                    form.namespaces.includes(ns.name)
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {ns.name}
                </button>
              ))
            )}
          </div>
          {form.namespaces.length === 0 && (
            <p className="text-xs text-muted-foreground">No namespaces selected (will collect from all)</p>
          )}
        </div>
      )}

      <div className="space-y-2">
        <p className="text-sm font-medium text-foreground">Label Selectors</p>
        <div className="flex gap-2">
          <Input
            aria-label="Label key"
            value={form.labelKey}
            onChange={(e) => pipelineForm.setFieldValue('labelKey', e.target.value)}
            placeholder="Label key"
            className="h-8 font-mono text-xs"
          />
          <Input
            aria-label="Label value"
            value={form.labelValue}
            onChange={(e) => pipelineForm.setFieldValue('labelValue', e.target.value)}
            placeholder="Value"
            className="h-8 font-mono text-xs"
          />
          <ActionButton
            size="icon"
            onClick={addLabel}
            disabled={!form.labelKey || !form.labelValue}
            icon={<Plus className="h-3.5 w-3.5" />}
            title="Add label"
          />
        </div>
        {Object.entries(form.labels).length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(form.labels).map(([k, v]) => (
              <span
                key={k}
                className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
              >
                {k}={v}
                <button type="button" onClick={() => removeLabel(k)} className="hover:text-foreground">
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="space-y-1.5">
        <p className="text-sm font-medium text-foreground">Outputs</p>
        <div className="space-y-1.5 max-h-40 overflow-y-auto p-2 rounded-md border border-border bg-background">
          {outputsLoading ? (
            <span className="text-xs text-muted-foreground">Loading outputs…</span>
          ) : outputList.length === 0 ? (
            <span className="text-xs text-muted-foreground">No outputs available. Create an output first.</span>
          ) : (
            outputList.map((output) => (
              <label
                key={output.id}
                className="flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent cursor-pointer"
              >
                <input
                  type="checkbox"
                  checked={form.outputIds.includes(output.id)}
                  onChange={() => toggleOutput(output.id)}
                  className="rounded border-border text-primary focus:ring-ring"
                />
                <span className="text-foreground">{output.name}</span>
                <span className="text-xs text-muted-foreground capitalize">({output.type})</span>
              </label>
            ))
          )}
        </div>
      </div>

      <label className="flex items-center gap-2 cursor-pointer">
        <pipelineForm.Field name="enabled">
          {(field) => (
            <input
              type="checkbox"
              checked={field.state.value}
              onChange={(e) => field.handleChange(e.target.checked)}
              onBlur={field.handleBlur}
              className="rounded border-border text-primary focus:ring-ring"
            />
          )}
        </pipelineForm.Field>
        <span className="text-sm text-foreground">Enabled</span>
      </label>
    </ModalShell>
  );
}
