'use client';

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

import { useEffect, useState } from 'react';
import { Link } from '@/lib/link';
import { ArrowLeft, Plus, Trash2, Save, Loader2, FlaskConical, CheckCircle, XCircle } from 'lucide-react';
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
  type WidgetWriteBody,
  type PrometheusDatasource,
} from '@/lib/api/dashboards';

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

export default function WidgetsAdminPage() {
  const [widgets, setWidgets] = useState<Widget[]>([]);
  const [datasources, setDatasources] = useState<PrometheusDatasource[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<Partial<Widget> | null>(null);
  const [specText, setSpecText] = useState('');
  const [saving, setSaving] = useState(false);
  const [dsError, setDsError] = useState<string | null>(null);
  const [testStatus, setTestStatus] = useState<Record<string, { ok: boolean; msg: string }>>({});

  const reload = async () => {
    try {
      const [ws, ds] = await Promise.all([listWidgets(), listDatasources()]);
      setWidgets(ws);
      setDatasources(ds);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void reload();
  }, []);

  const startCreate = () => {
    const t: WidgetType = 'prom_sparkline';
    setEditing({
      name: '',
      description: '',
      widgetType: t,
      scope: 'global',
      scopeIds: [],
      grid: { x: 0, y: 0, w: 4, h: 2 },
      refreshSeconds: 60,
      enabled: true,
    });
    setSpecText(DEFAULT_SPEC_BY_TYPE[t]);
  };

  const startEdit = (w: Widget) => {
    setEditing({ ...w });
    setSpecText(JSON.stringify(w.spec, null, 2));
  };

  const cancel = () => {
    setEditing(null);
    setSpecText('');
  };

  const submit = async () => {
    if (!editing) return;
    setSaving(true);
    try {
      let spec: any = {};
      try {
        spec = JSON.parse(specText);
      } catch (e) {
        setError('Spec is not valid JSON');
        setSaving(false);
        return;
      }
      const body: WidgetWriteBody = {
        name: editing.name ?? '',
        description: editing.description ?? '',
        widget_type: (editing.widgetType ?? 'prom_sparkline') as WidgetType,
        spec,
        scope: (editing.scope ?? 'global') as WidgetScope,
        scope_ids: editing.scopeIds ?? [],
        grid: editing.grid ?? { x: 0, y: 0, w: 4, h: 2 },
        refresh_seconds: editing.refreshSeconds ?? 60,
        enabled: editing.enabled ?? true,
      };
      if (editing.id) {
        await updateWidget(editing.id, body);
      } else {
        await createWidget(body);
      }
      setEditing(null);
      setSpecText('');
      await reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (w: Widget) => {
    if (!confirm(`Delete widget "${w.name}"?`)) return;
    try {
      await deleteWidget(w.id);
      await reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  const [dsName, setDsName] = useState('');
  const [dsURL, setDsURL] = useState('');
  const [dsBearer, setDsBearer] = useState('');

  const addDS = async () => {
    setDsError(null);
    try {
      await createDatasource({ name: dsName, url: dsURL, bearer_token: dsBearer, enabled: true });
      setDsName('');
      setDsURL('');
      setDsBearer('');
      await reload();
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

  const removeDS = async (id: string) => {
    if (!confirm('Delete datasource?')) return;
    try {
      await deleteDatasource(id);
      await reload();
    } catch (e) {
      setDsError(e instanceof Error ? e.message : String(e));
    }
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

      {error ? <div className="text-sm text-red-600">{error}</div> : null}

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
                <input
                  className="w-full bg-background border border-border rounded px-2 py-1"
                  value={editing.name ?? ''}
                  onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                />
              </label>
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Widget type</div>
                <select
                  className="w-full bg-background border border-border rounded px-2 py-1"
                  value={editing.widgetType ?? 'prom_sparkline'}
                  onChange={(e) => {
                    const t = e.target.value as WidgetType;
                    setEditing({ ...editing, widgetType: t });
                    setSpecText(DEFAULT_SPEC_BY_TYPE[t]);
                  }}
                >
                  <option value="prom_sparkline">Prometheus sparkline</option>
                  <option value="prom_stat">Prometheus stat</option>
                  <option value="grafana_panel">Grafana panel</option>
                  <option value="url_iframe">URL iframe</option>
                </select>
              </label>
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Scope</div>
                <select
                  className="w-full bg-background border border-border rounded px-2 py-1"
                  value={editing.scope ?? 'global'}
                  onChange={(e) => setEditing({ ...editing, scope: e.target.value as WidgetScope })}
                >
                  <option value="global">Global</option>
                  <option value="cluster">Cluster</option>
                  <option value="project">Project</option>
                </select>
              </label>
              <label className="text-sm">
                <div className="text-muted-foreground mb-1">Refresh seconds</div>
                <input
                  type="number"
                  className="w-full bg-background border border-border rounded px-2 py-1"
                  value={editing.refreshSeconds ?? 60}
                  onChange={(e) => setEditing({ ...editing, refreshSeconds: parseInt(e.target.value, 10) || 60 })}
                />
              </label>
              <label className="text-sm col-span-2">
                <div className="text-muted-foreground mb-1">Grid (x, y, w, h)</div>
                <div className="flex gap-2">
                  {(['x', 'y', 'w', 'h'] as const).map((k) => (
                    <input
                      key={k}
                      type="number"
                      className="w-20 bg-background border border-border rounded px-2 py-1"
                      value={editing.grid?.[k] ?? 0}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          grid: { ...(editing.grid ?? { x: 0, y: 0, w: 4, h: 2 }), [k]: parseInt(e.target.value, 10) || 0 },
                        })
                      }
                    />
                  ))}
                </div>
              </label>
            </div>
            <label className="text-sm block">
              <div className="text-muted-foreground mb-1">Spec (JSON)</div>
              <textarea
                rows={10}
                className="w-full font-mono text-xs bg-background border border-border rounded p-2"
                value={specText}
                onChange={(e) => setSpecText(e.target.value)}
              />
            </label>
            <div className="flex gap-2">
              <button onClick={submit} disabled={saving} className="inline-flex items-center gap-1 bg-primary text-primary-foreground text-sm px-3 py-1.5 rounded">
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
                      <button onClick={() => remove(w)} className="text-xs text-red-600 hover:underline inline-flex items-center gap-1">
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
        {dsError ? <div className="text-sm text-red-600">{dsError}</div> : null}
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
                    <button onClick={() => removeDS(d.id)} className="text-xs text-red-600 hover:underline">
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
    </div>
  );
}
