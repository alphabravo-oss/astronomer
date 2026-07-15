import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/network-policies — admin CRUD for network policy
 * templates (migration 068).
 *
 * Lists the four built-in templates (deny_all_ingress, project_isolated,
 * namespace_only, allow_ingress_controllers) plus any custom rows the
 * operator has cloned. Builtin rows are read-only and rendered with a
 * "Clone" action instead of Edit/Delete. Custom rows are editable.
 *
 * Superuser-only at the API layer; the SPA gates the navigation entry
 * behind the same useIsSuperuser hook as the rest of the settings hub.
 */

import { useEffect, useState } from 'react';
import { Link } from '@/lib/link';
import { ArrowLeft, Plus, Trash2, Save, Copy, Loader2, ShieldCheck } from 'lucide-react';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  listNetworkPolicyTemplates,
  createNetworkPolicyTemplate,
  updateNetworkPolicyTemplate,
  deleteNetworkPolicyTemplate,
  type NetworkPolicyTemplate,
  type NetworkPolicyTemplateWriteRequest,
} from '@/lib/api/settings';

function KindBadge({ kind }: { kind: 'builtin' | 'custom' }) {
  const palette =
    kind === 'builtin'
      ? 'bg-indigo-500/10 text-indigo-600 dark:text-indigo-400 border-indigo-500/30'
      : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/30';
  return (
    <span className={`text-xs px-2 py-0.5 rounded border font-medium uppercase ${palette}`}>{kind}</span>
  );
}

function TemplateRow({
  tmpl,
  onClone,
  onEdit,
  onDelete,
}: {
  tmpl: NetworkPolicyTemplate;
  onClone: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <TableRow className="border-b border-border last:border-0">
      <TableCell className="px-3 py-3 align-top">
        <div className="font-medium text-foreground">{tmpl.name}</div>
        <div className="text-xs text-muted-foreground font-mono">{tmpl.slug}</div>
      </TableCell>
      <TableCell className="px-3 py-3 align-top">
        <KindBadge kind={tmpl.kind} />
      </TableCell>
      <TableCell className="px-3 py-3 align-top text-sm text-muted-foreground max-w-md">{tmpl.description}</TableCell>
      <TableCell className="px-3 py-3 align-top">
        <span
          className={`text-xs px-2 py-0.5 rounded border font-medium ${
            tmpl.enabled
              ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/30'
              : 'bg-muted text-muted-foreground border-border'
          }`}
        >
          {tmpl.enabled ? 'enabled' : 'disabled'}
        </span>
      </TableCell>
      <TableCell className="px-3 py-3 align-top text-right">
        <div className="flex items-center justify-end gap-1">
          <button
            type="button"
            className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border border-border hover:bg-muted"
            onClick={onClone}
            title="Create an editable copy"
          >
            <Copy className="h-3 w-3" /> Clone
          </button>
          {tmpl.kind === 'custom' && (
            <>
              <button
                type="button"
                className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border border-border hover:bg-muted"
                onClick={onEdit}
              >
                Edit
              </button>
              <button
                type="button"
                className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded border border-rose-500/30 text-rose-600 dark:text-rose-400 hover:bg-rose-500/10"
                onClick={onDelete}
              >
                <Trash2 className="h-3 w-3" />
              </button>
            </>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}

interface DraftForm extends NetworkPolicyTemplateWriteRequest {
  id?: string;
}

function NetworkPoliciesPanel() {
  const [templates, setTemplates] = useState<NetworkPolicyTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [draft, setDraft] = useState<DraftForm | null>(null);
  const [saving, setSaving] = useState(false);

  const refresh = async () => {
    setLoading(true);
    try {
      const items = await listNetworkPolicyTemplates();
      setTemplates(items);
    } catch (err: unknown) {
      toastApiError('Failed to load templates', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const handleClone = (tmpl: NetworkPolicyTemplate) => {
    setDraft({
      clone_from: tmpl.slug,
      slug: `${tmpl.slug}_copy`,
      name: `${tmpl.name} (copy)`,
      description: tmpl.description,
      spec_template: tmpl.spec_template,
      enabled: true,
    });
  };

  const handleEdit = (tmpl: NetworkPolicyTemplate) => {
    setDraft({
      id: tmpl.id,
      slug: tmpl.slug,
      name: tmpl.name,
      description: tmpl.description,
      spec_template: tmpl.spec_template,
      enabled: tmpl.enabled,
    });
  };

  const handleDelete = async (tmpl: NetworkPolicyTemplate) => {
    if (!confirm(`Delete custom template "${tmpl.name}"?`)) return;
    try {
      await deleteNetworkPolicyTemplate(tmpl.id);
      toastSuccess('Template deleted');
      await refresh();
    } catch (err: unknown) {
      toastApiError('Delete failed', err);
    }
  };

  const handleSave = async () => {
    if (!draft) return;
    setSaving(true);
    try {
      if (draft.id) {
        await updateNetworkPolicyTemplate(draft.id, {
          name: draft.name,
          description: draft.description,
          spec_template: draft.spec_template,
          enabled: draft.enabled,
        });
        toastSuccess('Template updated');
      } else {
        await createNetworkPolicyTemplate(draft);
        toastSuccess('Template created');
      }
      setDraft(null);
      await refresh();
    } catch (err: unknown) {
      toastApiError('Save failed', err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <Link href="/dashboard/settings" className="inline-flex items-center text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4 mr-1" /> Back to settings
        </Link>
        <button
          type="button"
          onClick={() =>
            setDraft({
              slug: '',
              name: '',
              description: '',
              spec_template:
                'apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: {{.PolicyName}}\n  namespace: {{.Namespace}}\nspec:\n  podSelector: {}\n  policyTypes: [Ingress]\n',
              enabled: true,
            })
          }
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded border border-border bg-card hover:bg-muted"
        >
          <Plus className="h-4 w-4" /> New custom template
        </button>
      </div>

      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight flex items-center gap-2">
          <ShieldCheck className="h-5 w-5" /> Network policy templates
        </h1>
        <p className="text-sm text-muted-foreground mt-1 max-w-3xl">
          Pre-built Kubernetes NetworkPolicy bundles. Built-in rows are read-only — clone to
          create an editable custom row. Apply templates to namespaces from the cluster detail
          page&apos;s Network policies tab.
        </p>
      </div>

      {loading ? (
        <div className="flex items-center text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 mr-2 animate-spin" /> Loading templates...
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-border">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted">
              <TableRow className="text-left">
                <TableHead className="px-3 py-2 font-medium">Template</TableHead>
                <TableHead className="px-3 py-2 font-medium">Kind</TableHead>
                <TableHead className="px-3 py-2 font-medium">Description</TableHead>
                <TableHead className="px-3 py-2 font-medium">Status</TableHead>
                <TableHead className="px-3 py-2 text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {templates.map((t) => (
                <TemplateRow
                  key={t.id}
                  tmpl={t}
                  onClone={() => handleClone(t)}
                  onEdit={() => handleEdit(t)}
                  onDelete={() => handleDelete(t)}
                />
              ))}
              {templates.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="px-3 py-6 text-center text-sm text-muted-foreground">
                    No templates. Run migration 068 to seed the built-ins.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}

      {draft && (
        <div className="rounded-lg border border-border bg-card p-4 space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-semibold">{draft.id ? 'Edit template' : 'New template'}</h2>
            <button type="button" className="text-xs text-muted-foreground hover:text-foreground" onClick={() => setDraft(null)}>
              Cancel
            </button>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <label className="text-sm space-y-1">
              <span className="text-muted-foreground">Slug</span>
              <input
                type="text"
                className="w-full px-2 py-1 rounded border border-border bg-background font-mono text-sm"
                value={draft.slug ?? ''}
                disabled={!!draft.id}
                onChange={(e) => setDraft({ ...draft, slug: e.target.value })}
                placeholder="my_custom_policy"
              />
            </label>
            <label className="text-sm space-y-1">
              <span className="text-muted-foreground">Name</span>
              <input
                type="text"
                className="w-full px-2 py-1 rounded border border-border bg-background text-sm"
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </label>
          </div>
          <label className="text-sm space-y-1 block">
            <span className="text-muted-foreground">Description</span>
            <textarea
              className="w-full px-2 py-1 rounded border border-border bg-background text-sm"
              rows={2}
              value={draft.description ?? ''}
              onChange={(e) => setDraft({ ...draft, description: e.target.value })}
            />
          </label>
          <label className="text-sm space-y-1 block">
            <span className="text-muted-foreground">Spec template (Go text/template + YAML)</span>
            <textarea
              className="w-full px-2 py-1 rounded border border-border bg-background text-xs font-mono"
              rows={14}
              value={draft.spec_template}
              onChange={(e) => setDraft({ ...draft, spec_template: e.target.value })}
            />
            <span className="text-xs text-muted-foreground">
              Variables: <code className="font-mono">{'{{.Namespace}}'}</code>,{' '}
              <code className="font-mono">{'{{.Project}}'}</code>,{' '}
              <code className="font-mono">{'{{.PolicyName}}'}</code>
            </span>
          </label>
          <label className="inline-flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={draft.enabled ?? true}
              onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            />
            Enabled
          </label>
          <div>
            <button
              type="button"
              onClick={handleSave}
              disabled={saving}
              className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded border border-border bg-foreground text-background hover:opacity-90 disabled:opacity-50"
            >
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
              Save
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function NetworkPoliciesSettingsPage() {
  return (
    <SettingsAuthGate>
      <NetworkPoliciesPanel />
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/network-policies/')({
  component: NetworkPoliciesSettingsPage,
});
