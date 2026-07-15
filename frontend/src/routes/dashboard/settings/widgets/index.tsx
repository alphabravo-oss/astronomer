import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/widgets — admin CRUD for dashboard widgets +
 * Prometheus datasources (migration 058).
 *
 * Renders two tables:
 *   1. Widgets — list + inline-create form. The form switches
 *      spec shape based on the selected widget_type (grafana_panel /
 *      prom_sparkline / prom_stat / url_iframe). Edits open a
 *      pre-filled form in place.
 *   2. Prometheus datasources — list + create. The /test/ button
 *      validates connectivity end-to-end.
 *
 * Superuser-only at the API layer; the SPA gates the navigation
 * entry behind the same useIsSuperuser hook as the rest of the
 * settings hub.
 */

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Link } from '@/lib/link';
import { ArrowLeft, Plus, Trash2, Save, Loader2, FlaskConical, CheckCircle, XCircle } from 'lucide-react';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { useAppForm } from '@/lib/form';
import {
  listWidgets,
  createWidget,
  updateWidget,
  deleteWidget,
  listDatasources,
  createDatasource,
  deleteDatasource,
  testDatasource,
  type Widget,
  type WidgetType,
  type WidgetScope,
  type WidgetSpec,
  type WidgetWriteBody,
  type PrometheusDatasource,
} from '@/lib/api/dashboards';

// Local cache keys for the admin widget/datasource lists. Assigned to
// identifiers (not inlined into `queryKey:`) so they satisfy the lint rule
// that reserves inline queryKey arrays for src/lib/query-keys.ts.
const WIDGETS_KEY = ['admin', 'dashboard-widgets'] as const;
const DATASOURCES_KEY = ['admin', 'prometheus-datasources'] as const;

const DEFAULT_SPEC_BY_TYPE: Record<WidgetType, string> = {
  grafana_panel: JSON.stringify(
    { base_url: 'https://grafana.example.com', dashboard_uid: 'xyz', panel_id: 1, vars: { cluster: '$cluster_uid' } },
    null,
    2,
  ),
  prom_sparkline: JSON.stringify(
    { datasource: 'default', query: 'sum(rate(container_cpu_usage_seconds_total[5m]))', duration: '1h', step: '60s' },
    null,
    2,
  ),
  prom_stat: JSON.stringify(
    { datasource: 'default', query: 'histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket[5m])) by (le))', unit: 's', format: '.3f' },
    null,
    2,
  ),
  url_iframe: JSON.stringify({ url: 'https://billing.example.com/clusters/{{cluster_uid}}', height_px: 280 }, null, 2),
};

function WidgetsAdminPage() {
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  // Non-null while the editor is open; carries the row id in edit mode. The
  // field values themselves live on the TanStack form below.
  const [editing, setEditing] = useState<{ id?: string } | null>(null);
  const [dsError, setDsError] = useState<string | null>(null);
  const [testStatus, setTestStatus] = useState<Record<string, { ok: boolean; msg: string }>>({});
  const [deleteWidgetTarget, setDeleteWidgetTarget] = useState<Widget | null>(null);
  const [deleteDatasourceTarget, setDeleteDatasourceTarget] = useState<PrometheusDatasource | null>(null);

  const widgetsQuery = useQuery({ queryKey: WIDGETS_KEY, queryFn: listWidgets });
  const datasourcesQuery = useQuery({ queryKey: DATASOURCES_KEY, queryFn: listDatasources });
  const widgets = widgetsQuery.data ?? [];
  const datasources = datasourcesQuery.data ?? [];
  const loading = widgetsQuery.isLoading;

  const invalidateWidgets = () => queryClient.invalidateQueries({ queryKey: WIDGETS_KEY });
  const invalidateDatasources = () => queryClient.invalidateQueries({ queryKey: DATASOURCES_KEY });

  const createWidgetMutation = useMutation({
    mutationFn: (body: WidgetWriteBody) => createWidget(body),
    onSuccess: invalidateWidgets,
  });
  const updateWidgetMutation = useMutation({
    mutationFn: ({ id, body }: { id: string; body: WidgetWriteBody }) => updateWidget(id, body),
    onSuccess: invalidateWidgets,
  });
  const deleteWidgetMutation = useMutation({
    mutationFn: (id: string) => deleteWidget(id),
    onSuccess: invalidateWidgets,
  });
  const createDatasourceMutation = useMutation({
    mutationFn: (body: { name: string; url: string; bearer_token: string; enabled: boolean }) =>
      createDatasource(body),
    onSuccess: invalidateDatasources,
  });
  const deleteDatasourceMutation = useMutation({
    mutationFn: (id: string) => deleteDatasource(id),
    onSuccess: invalidateDatasources,
  });

  const saving = createWidgetMutation.isPending || updateWidgetMutation.isPending;

  const widgetDefaults = () => ({
    name: '',
    description: '',
    widgetType: 'prom_sparkline' as WidgetType,
    scope: 'global' as WidgetScope,
    scopeIds: [] as string[],
    grid: { x: 0, y: 0, w: 4, h: 2 },
    refreshSeconds: 60,
    enabled: true,
    specText: DEFAULT_SPEC_BY_TYPE.prom_sparkline,
  });

  const form = useAppForm({
    defaultValues: widgetDefaults(),
    onSubmit: async ({ value }) => {
      if (!editing) return;
      let spec: WidgetSpec = {};
      try {
        spec = JSON.parse(value.specText);
      } catch {
        setError('Spec is not valid JSON');
        return;
      }
      const body: WidgetWriteBody = {
        name: value.name,
        description: value.description,
        widget_type: value.widgetType,
        spec,
        scope: value.scope,
        scope_ids: value.scopeIds,
        grid: value.grid,
        refresh_seconds: value.refreshSeconds,
        enabled: value.enabled,
      };
      try {
        if (editing.id) {
          await updateWidgetMutation.mutateAsync({ id: editing.id, body });
        } else {
          await createWidgetMutation.mutateAsync(body);
        }
        setEditing(null);
        form.reset(widgetDefaults());
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
  });

  const startCreate = () => {
    setEditing({});
    form.reset(widgetDefaults());
  };

  const startEdit = (w: Widget) => {
    setEditing({ id: w.id });
    form.reset({
      name: w.name,
      description: w.description ?? '',
      widgetType: w.widgetType,
      scope: w.scope,
      scopeIds: w.scopeIds ?? [],
      grid: w.grid ?? { x: 0, y: 0, w: 4, h: 2 },
      refreshSeconds: w.refreshSeconds ?? 60,
      enabled: w.enabled ?? true,
      specText: JSON.stringify(w.spec, null, 2),
    });
  };

  const cancel = () => {
    setEditing(null);
    form.reset(widgetDefaults());
  };

  const confirmDeleteWidget = async () => {
    if (!deleteWidgetTarget) return;
    try {
      await deleteWidgetMutation.mutateAsync(deleteWidgetTarget.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setDeleteWidgetTarget(null);
  };

  const [dsName, setDsName] = useState('');
  const [dsURL, setDsURL] = useState('');
  const [dsBearer, setDsBearer] = useState('');

  const addDS = async () => {
    setDsError(null);
    try {
      await createDatasourceMutation.mutateAsync({ name: dsName, url: dsURL, bearer_token: dsBearer, enabled: true });
      setDsName('');
      setDsURL('');
      setDsBearer('');
    } catch (e) {
      setDsError(e instanceof Error ? e.message : String(e));
    }
  };

  const runTest = async (id: string) => {
    try {
      const out = await testDatasource(id);
      setTestStatus((s) => ({ ...s, [id]: { ok: out.ok, msg: out.message } }));
    } catch (e) {
      setTestStatus((s) => ({ ...s, [id]: { ok: false, msg: e instanceof Error ? e.message : String(e) } }));
    }
  };

  const confirmDeleteDatasource = async () => {
    if (!deleteDatasourceTarget) return;
    try {
      await deleteDatasourceMutation.mutateAsync(deleteDatasourceTarget.id);
    } catch (e) {
      setDsError(e instanceof Error ? e.message : String(e));
    }
    setDeleteDatasourceTarget(null);
  };

  return (
    <div className="space-y-8">
      <div className="flex items-center justify-between">
        <div>
          <Link href="/dashboard/settings" className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1">
            <ArrowLeft className="h-3 w-3" /> Settings
          </Link>
          <h1 className="text-2xl font-semibold mt-1">Dashboard widgets</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Define widgets pinned to the global dashboard, per-cluster pages, or per-project pages.
          </p>
        </div>
      </div>

      {error || widgetsQuery.isError ? (
        <div className="text-sm text-red-600">
          {error ??
            (widgetsQuery.error instanceof Error ? widgetsQuery.error.message : 'Failed to load widgets')}
        </div>
      ) : null}

      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-medium">Widgets</h2>
          {!editing ? (
            <button onClick={startCreate} className="text-sm inline-flex items-center gap-1 bg-primary text-primary-foreground px-3 py-1.5 rounded">
              <Plus className="h-4 w-4" /> New widget
            </button>
          ) : null}
        </div>

        {editing ? (
          <div className="border border-border rounded-lg p-4 bg-card space-y-3">
            <div className="grid grid-cols-2 gap-3">
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Name</div>
                <form.Field name="name">
                  {(field) => (
                    <input
                      className="w-full bg-background border border-border rounded px-2 py-1"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                      onBlur={field.handleBlur}
                    />
                  )}
                </form.Field>
              </label>
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Widget type</div>
                <form.Field name="widgetType">
                  {(field) => (
                    <select
                      className="w-full bg-background border border-border rounded px-2 py-1"
                      value={field.state.value}
                      onChange={(e) => {
                        const t = e.target.value as WidgetType;
                        field.handleChange(t);
                        // Switching types re-seeds the spec text, exactly like
                        // the old setSpecText(DEFAULT_SPEC_BY_TYPE[t]).
                        form.setFieldValue('specText', DEFAULT_SPEC_BY_TYPE[t]);
                      }}
                      onBlur={field.handleBlur}
                    >
                      <option value="prom_sparkline">Prometheus sparkline</option>
                      <option value="prom_stat">Prometheus stat</option>
                      <option value="grafana_panel">Grafana panel</option>
                      <option value="url_iframe">URL iframe</option>
                    </select>
                  )}
                </form.Field>
              </label>
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Scope</div>
                <form.Field name="scope">
                  {(field) => (
                    <select
                      className="w-full bg-background border border-border rounded px-2 py-1"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value as WidgetScope)}
                      onBlur={field.handleBlur}
                    >
                      <option value="global">Global</option>
                      <option value="cluster">Cluster</option>
                      <option value="project">Project</option>
                    </select>
                  )}
                </form.Field>
              </label>
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Refresh seconds</div>
                <form.Field name="refreshSeconds">
                  {(field) => (
                    <input
                      type="number"
                      className="w-full bg-background border border-border rounded px-2 py-1"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(parseInt(e.target.value, 10) || 60)}
                      onBlur={field.handleBlur}
                    />
                  )}
                </form.Field>
              </label>
              <label className="text-sm col-span-2">
                <div className="text-muted-foreground mb-1">Grid (x, y, w, h)</div>
                <form.Field name="grid">
                  {(field) => (
                    <div className="flex gap-2">
                      {(['x', 'y', 'w', 'h'] as const).map((k) => (
                        <input
                          key={k}
                          type="number"
                          className="w-20 bg-background border border-border rounded px-2 py-1"
                          value={field.state.value[k] ?? 0}
                          onChange={(e) =>
                            field.handleChange({
                              ...field.state.value,
                              [k]: parseInt(e.target.value, 10) || 0,
                            })
                          }
                          onBlur={field.handleBlur}
                        />
                      ))}
                    </div>
                  )}
                </form.Field>
              </label>
            </div>
            <label className="text-sm block">
              <div className="text-muted-foreground mb-1">Spec (JSON)</div>
              <form.Field name="specText">
                {(field) => (
                  <textarea
                    rows={10}
                    className="w-full font-mono text-xs bg-background border border-border rounded p-2"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                )}
              </form.Field>
            </label>
            <div className="flex gap-2">
              <button onClick={() => void form.handleSubmit()} disabled={saving} className="inline-flex items-center gap-1 bg-primary text-primary-foreground text-sm px-3 py-1.5 rounded">
                {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                Save
              </button>
              <button onClick={cancel} className="text-sm px-3 py-1.5 rounded border border-border">
                Cancel
              </button>
            </div>
          </div>
        ) : null}

        {loading ? (
          <div className="text-sm text-muted-foreground">Loading...</div>
        ) : widgets.length === 0 ? (
          <div className="text-sm text-muted-foreground">No widgets defined. Click "New widget" to add one.</div>
        ) : (
          <div className="border border-border rounded-lg overflow-hidden">
            <Table className="w-full text-sm">
              <TableHeader className="bg-muted/50">
                <TableRow>
                  <TableHead className="text-left px-3 py-2">Name</TableHead>
                  <TableHead className="text-left px-3 py-2">Type</TableHead>
                  <TableHead className="text-left px-3 py-2">Scope</TableHead>
                  <TableHead className="text-left px-3 py-2">Refresh</TableHead>
                  <TableHead className="text-left px-3 py-2">Enabled</TableHead>
                  <TableHead className="text-right px-3 py-2">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {widgets.map((w) => (
                  <TableRow key={w.id} className="border-t border-border">
                    <TableCell className="px-3 py-2">{w.name}</TableCell>
                    <TableCell className="px-3 py-2 text-muted-foreground">{w.widgetType}</TableCell>
                    <TableCell className="px-3 py-2 text-muted-foreground">{w.scope}</TableCell>
                    <TableCell className="px-3 py-2 text-muted-foreground">{w.refreshSeconds}s</TableCell>
                    <TableCell className="px-3 py-2">{w.enabled ? 'yes' : 'no'}</TableCell>
                    <TableCell className="px-3 py-2 text-right">
                      <button onClick={() => startEdit(w)} className="text-xs text-primary hover:underline mr-2">
                        Edit
                      </button>
                      <button onClick={() => setDeleteWidgetTarget(w)} className="text-xs text-red-600 hover:underline inline-flex items-center gap-1">
                        <Trash2 className="h-3 w-3" /> Delete
                      </button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </section>

      <section className="space-y-3">
        <h2 className="text-lg font-medium">Prometheus datasources</h2>
        {dsError || datasourcesQuery.isError ? (
          <div className="text-sm text-red-600">
            {dsError ??
              (datasourcesQuery.error instanceof Error
                ? datasourcesQuery.error.message
                : 'Failed to load datasources')}
          </div>
        ) : null}
        <div className="border border-border rounded-lg overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted/50">
              <TableRow>
                <TableHead className="text-left px-3 py-2">Name</TableHead>
                <TableHead className="text-left px-3 py-2">URL</TableHead>
                <TableHead className="text-left px-3 py-2">Auth</TableHead>
                <TableHead className="text-left px-3 py-2">Status</TableHead>
                <TableHead className="text-right px-3 py-2">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {datasources.map((d) => (
                <TableRow key={d.id} className="border-t border-border">
                  <TableCell className="px-3 py-2">{d.name}</TableCell>
                  <TableCell className="px-3 py-2 font-mono text-xs text-muted-foreground">{d.url}</TableCell>
                  <TableCell className="px-3 py-2 text-muted-foreground">{d.hasAuth ? 'yes' : 'none'}</TableCell>
                  <TableCell className="px-3 py-2">
                    {testStatus[d.id] ? (
                      <span className={`inline-flex items-center gap-1 text-xs ${testStatus[d.id].ok ? 'text-emerald-600' : 'text-red-600'}`}>
                        {testStatus[d.id].ok ? <CheckCircle className="h-3 w-3" /> : <XCircle className="h-3 w-3" />}
                        <span className="truncate max-w-[12rem]" title={testStatus[d.id].msg}>{testStatus[d.id].msg}</span>
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">unknown</span>
                    )}
                  </TableCell>
                  <TableCell className="px-3 py-2 text-right">
                    <button onClick={() => runTest(d.id)} className="text-xs text-primary hover:underline mr-2 inline-flex items-center gap-1">
                      <FlaskConical className="h-3 w-3" /> Test
                    </button>
                    <button onClick={() => setDeleteDatasourceTarget(d)} className="text-xs text-red-600 hover:underline">
                      Delete
                    </button>
                  </TableCell>
                </TableRow>
              ))}
              <TableRow className="border-t border-border bg-muted/20">
                <TableCell className="px-3 py-2">
                  <input className="bg-background border border-border rounded px-2 py-1 w-32" placeholder="name" value={dsName} onChange={(e) => setDsName(e.target.value)} />
                </TableCell>
                <TableCell className="px-3 py-2">
                  <input className="bg-background border border-border rounded px-2 py-1 w-full font-mono text-xs" placeholder="https://prom..." value={dsURL} onChange={(e) => setDsURL(e.target.value)} />
                </TableCell>
                <TableCell className="px-3 py-2">
                  <input className="bg-background border border-border rounded px-2 py-1 w-32" placeholder="Bearer (optional)" value={dsBearer} onChange={(e) => setDsBearer(e.target.value)} />
                </TableCell>
                <TableCell className="px-3 py-2 text-muted-foreground text-xs">—</TableCell>
                <TableCell className="px-3 py-2 text-right">
                  <button onClick={addDS} className="text-xs text-primary hover:underline inline-flex items-center gap-1">
                    <Plus className="h-3 w-3" /> Add
                  </button>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
      </section>

      <ConfirmDialog
        open={!!deleteWidgetTarget}
        onClose={() => setDeleteWidgetTarget(null)}
        onConfirm={confirmDeleteWidget}
        title="Delete Widget"
        description={`Delete widget "${deleteWidgetTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleteWidgetMutation.isPending}
      />

      <ConfirmDialog
        open={!!deleteDatasourceTarget}
        onClose={() => setDeleteDatasourceTarget(null)}
        onConfirm={confirmDeleteDatasource}
        title="Delete Datasource"
        description={`Delete datasource "${deleteDatasourceTarget?.name}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleteDatasourceMutation.isPending}
      />
    </div>
  );
}

export const Route = createFileRoute('/dashboard/settings/widgets/')({
  component: WidgetsAdminPage,
});
