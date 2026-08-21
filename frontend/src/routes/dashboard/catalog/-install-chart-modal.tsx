import { useEffect, useMemo, useState } from 'react';
import { HelmValuesForm } from '@/components/catalog/helm-values-form';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { useAppForm, useStore } from '@/lib/form';
import { useClusters, useInstallHelmChart } from '@/lib/hooks';
import { useSearchParams } from '@/lib/navigation';
import {
  dumpHelmValuesYAML,
  hasRenderableSchema,
  resolveSchemaRefs,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from '@/lib/helm-values-schema';
import { cn } from '@/lib/utils';
import type { HelmChart, HelmChartVersion } from '@/types';
import { AlertTriangle, Braces, FileCode2 } from 'lucide-react';

export function InstallChartModal({
  projectId,
  allowedClusterIds,
  chart,
  version,
  onClose,
}: {
  projectId: string;
  allowedClusterIds: string[];
  chart: HelmChart;
  version: HelmChartVersion;
  onClose: () => void;
}) {
  const installChart = useInstallHelmChart();
  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = (clustersData?.data || []).filter((cluster) =>
    allowedClusterIds.includes(cluster.id),
  );
  const schema = useMemo(() => {
    // Inline $ref/$defs first so generator-style schemas (cert-manager etc.) render.
    const resolved = resolveSchemaRefs(version.valuesSchema);
    return hasRenderableSchema(resolved) ? (resolved as HelmValuesSchemaNode) : null;
  }, [version.valuesSchema]);

  // Sprint 23: when arriving from an empty-state CTA on a cluster
  // detail page (e.g. "Install trivy-operator from Image Scans"), the
  // URL carries ?cluster_id=<uuid>. Pre-populate the target dropdown so
  // the operator doesn't have to pick again. Empty when absent.
  const searchParams = useSearchParams();
  const presetClusterId = searchParams?.get('cluster_id') ?? '';

  const form = useAppForm({
    defaultValues: {
      clusterId: allowedClusterIds.includes(presetClusterId) ? presetClusterId : '',
      releaseName: chart.name,
      namespace: 'default',
      valuesOverride: version.defaultValues || '',
    },
    onSubmit: async ({ value }) => {
      try {
        await installChart.mutateAsync({
          project_id: projectId,
          cluster_id: value.clusterId,
          chart_version_id: version.id,
          release_name: value.releaseName,
          namespace: value.namespace,
          values_override: value.valuesOverride || undefined,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });
  const [editorMode, setEditorMode] = useState<'form' | 'yaml'>(schema ? 'form' : 'yaml');
  const [yamlError, setYamlError] = useState<string | null>(null);
  const [schemaValues, setSchemaValues] = useState<HelmValuesObject>(() => {
    const parsed = parseHelmValuesYAML(version.defaultValues || '') || {};
    return (schema ? mergeSchemaDefaults(schema, parsed) : parsed) as HelmValuesObject;
  });

  const clusterId = useStore(form.store, (s) => s.values.clusterId);
  const releaseName = useStore(form.store, (s) => s.values.releaseName);
  const namespace = useStore(form.store, (s) => s.values.namespace);

  useEffect(() => {
    const parsed = parseHelmValuesYAML(version.defaultValues || '') || {};
    form.setFieldValue('valuesOverride', version.defaultValues || '');
    setSchemaValues((schema ? mergeSchemaDefaults(schema, parsed) : parsed) as HelmValuesObject);
    setEditorMode(schema ? 'form' : 'yaml');
    setYamlError(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schema, version.defaultValues]);

  const handleSchemaValuesChange = (next: HelmValuesObject) => {
    setSchemaValues(next);
    form.setFieldValue('valuesOverride', dumpHelmValuesYAML(next));
    setYamlError(null);
  };

  const handleYAMLChange = (nextYAML: string) => {
    form.setFieldValue('valuesOverride', nextYAML);
    if (!schema) return;
    const parsed = parseHelmValuesYAML(nextYAML);
    if (parsed == null) {
      setYamlError('YAML must parse to an object before the form can stay in sync.');
      return;
    }
    setSchemaValues(mergeSchemaDefaults(schema, parsed) as HelmValuesObject);
    setYamlError(null);
  };

  const footer = (
    <>
      <ActionButton onClick={onClose}>Cancel</ActionButton>
      <ActionButton
        intent="primary"
        loading={installChart.isPending}
        disabled={!clusterId || !releaseName || !namespace}
        onClick={() => void form.handleSubmit()}
      >
        Install Chart
      </ActionButton>
    </>
  );

  return (
    <ModalShell
      title={`Install ${chart.displayName || chart.name}`}
      subtitle={`Version ${version.version}`}
      onClose={onClose}
      size="md"
      footer={footer}
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Target Cluster</label>
        <form.Field name="clusterId">
          {(field) => (
            <Select
              aria-label="Target Cluster"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            >
              <option value="">Select a cluster...</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.displayName} ({c.name})
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Release Name</label>
        <form.Field name="releaseName">
          {(field) => (
            <Input
              aria-label="Release Name"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="my-release"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Namespace</label>
        <form.Field name="namespace">
          {(field) => (
            <Input
              aria-label="Namespace"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="default"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <label className="text-sm font-medium text-foreground">Values Override</label>
            <p className="text-xs text-muted-foreground mt-1">
              {schema ? 'Edit with the chart schema form or switch to raw YAML.' : 'Raw YAML editor for chart values.'}
            </p>
          </div>
          {schema && (
            <div className="inline-flex rounded-md border border-border bg-muted/30 p-1">
              <button
                type="button"
                onClick={() => setEditorMode('form')}
                className={cn(
                  'inline-flex items-center gap-1 rounded px-2.5 py-1 text-xs font-medium transition-colors',
                  editorMode === 'form' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                <Braces className="h-3.5 w-3.5" />
                Form
              </button>
              <button
                type="button"
                onClick={() => setEditorMode('yaml')}
                className={cn(
                  'inline-flex items-center gap-1 rounded px-2.5 py-1 text-xs font-medium transition-colors',
                  editorMode === 'yaml' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                <FileCode2 className="h-3.5 w-3.5" />
                YAML
              </button>
            </div>
          )}
        </div>

        {schema && editorMode === 'form' ? (
          <div className="rounded-lg border border-border bg-muted/20 p-4">
            <HelmValuesForm schema={schema} value={schemaValues} onChange={handleSchemaValuesChange} />
          </div>
        ) : (
          <div className="space-y-2">
            <form.Field name="valuesOverride">
              {(field) => (
                <Textarea
                  aria-label="Values Override"
                  value={field.state.value}
                  onChange={(e) => handleYAMLChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="# Override default values here..."
                  rows={12}
                  className="min-h-0 resize-none text-sm"
                />
              )}
            </form.Field>
            {yamlError && (
              <div className="inline-flex items-center gap-2 rounded-md border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
                <AlertTriangle className="h-3.5 w-3.5" />
                {yamlError}
              </div>
            )}
          </div>
        )}
      </div>
    </ModalShell>
  );
}
