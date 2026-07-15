import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/templates/[key] — split-view template editor.
 *
 * Left:  built-in default (read-only, monospace).
 * Right: editable override textarea + subject input + enabled toggle.
 * Below: variables sidebar, sample-input JSON, Preview button, Save /
 *        Reset to default buttons.
 *
 * The Preview button POSTs to /preview/ with the operator's current
 * edits + sample variables. Backend enforces required-set and returns
 * a 400 with `missing` when applicable; we surface those names inline.
 */
import { useEffect, useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { Link } from '@/lib/link';
import { ArrowLeft, Eye, FileText, Loader2, RotateCcw, Save } from 'lucide-react';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { useAppForm } from '@/lib/form';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { EmptyState } from '@/components/ui/empty-state';
import {
  getNotificationTemplate,
  previewNotificationTemplate,
  resetNotificationTemplate,
  updateNotificationTemplate,
  type NotificationTemplateDetail,
  type NotificationTemplatePreviewResult,
} from '@/lib/api/settings';

function NotificationTemplateEditorPage() {
  return (
    <SettingsAuthGate>
      <NotificationTemplateEditor />
    </SettingsAuthGate>
  );
}

function NotificationTemplateEditor() {
  const params = useParams();
  const key = decodeURIComponent(String(params?.key ?? ''));
  const [detail, setDetail] = useState<NotificationTemplateDetail | null>(null);
  const [preview, setPreview] = useState<NotificationTemplatePreviewResult | null>(null);
  const [previewErr, setPreviewErr] = useState<string | null>(null);
  const [previewMissing, setPreviewMissing] = useState<string[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);

  // Override subject/body/enabled + the sample-variables JSON live on one
  // form; save uses the first three, Preview reads all of them.
  const form = useAppForm({
    defaultValues: { subject: '', body: '', enabled: true, samples: '{}' },
    onSubmit: async ({ value }) => {
      if (!detail) return;
      setSaving(true);
      try {
        const updated = await updateNotificationTemplate(key, {
          subject: value.subject,
          body: value.body,
          body_format: detail.bodyFormat,
          enabled: value.enabled,
        });
        setDetail(updated);
        toastSuccess('Template override saved');
      } catch (err) {
        toastApiError('', err, 'Save failed');
      } finally {
        setSaving(false);
      }
    },
  });

  useEffect(() => {
    if (!key) return;
    let cancelled = false;
    (async () => {
      try {
        const d = await getNotificationTemplate(key);
        if (cancelled) return;
        setDetail(d);
        form.reset({
          subject: d.subject,
          body: d.body,
          enabled: d.enabled,
          samples: seedSamples(d),
        });
      } catch (err) {
        toastApiError('', err, 'Failed to load template');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [form, key]);

  const handleReset = async () => {
    if (!detail) return;
    if (!window.confirm('Revert to the built-in default? Any saved override will be deleted.')) {
      return;
    }
    setSaving(true);
    try {
      await resetNotificationTemplate(key);
      // Re-fetch so the page shows the default again.
      const d = await getNotificationTemplate(key);
      setDetail(d);
      form.reset({
        subject: d.subject,
        body: d.body,
        enabled: true,
        samples: form.state.values.samples,
      });
      toastSuccess('Reverted to default');
    } catch (err) {
      toastApiError('', err, 'Reset failed');
    } finally {
      setSaving(false);
    }
  };

  const handlePreview = async () => {
    setPreviewing(true);
    setPreviewErr(null);
    setPreviewMissing(null);
    try {
      const { subject, body, samples } = form.state.values;
      const variables = JSON.parse(samples || '{}');
      const result = await previewNotificationTemplate(key, {
        subject,
        body,
        body_format: detail?.bodyFormat,
        variables,
      });
      setPreview(result);
    } catch (err: unknown) {
      // Try to surface the backend's structured 400 (missing[]).
      const e = err as { response?: { data?: { data?: { missing?: string[] }; missing?: string[] } } };
      const missing =
        e.response?.data?.data?.missing ?? e.response?.data?.missing ?? null;
      if (missing && missing.length > 0) {
        setPreviewMissing(missing);
        setPreviewErr('Sample variables are missing required entries.');
      } else {
        setPreviewErr(err instanceof Error ? err.message : 'Preview failed');
      }
      setPreview(null);
    } finally {
      setPreviewing(false);
    }
  };

  const variableList = useMemo(() => detail?.variables ?? [], [detail]);

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading…
      </div>
    );
  }
  if (!detail) {
    return (
      <EmptyState
        icon={FileText}
        title="Template not found"
        description="The notification template key may be invalid or no longer available."
        actionLabel="Back to templates"
        actionHref="/dashboard/settings/templates"
      />
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <Link
          href="/dashboard/settings/templates"
          className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
        >
          <ArrowLeft className="h-4 w-4" /> Notification templates
        </Link>
      </div>

      <div>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight">{detail.key}</h1>
        <p className="text-sm text-muted-foreground mt-1">{detail.description}</p>
        <div className="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
          <span className="px-2 py-0.5 rounded bg-muted">{detail.channel}</span>
          <span className="px-2 py-0.5 rounded bg-muted">{detail.bodyFormat}</span>
          {detail.hasOverride && (
            <span className="px-2 py-0.5 rounded bg-emerald-500/15 text-emerald-600">
              override active
            </span>
          )}
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="space-y-2">
          <label className="text-xs uppercase tracking-wide text-muted-foreground">
            Default subject
          </label>
          <pre className="rounded-md border border-border bg-muted/30 p-3 text-xs whitespace-pre-wrap overflow-auto">
            {detail.defaultSubject || <span className="text-muted-foreground">(none)</span>}
          </pre>
          <label className="text-xs uppercase tracking-wide text-muted-foreground">
            Default body
          </label>
          <pre className="rounded-md border border-border bg-muted/30 p-3 text-xs font-mono whitespace-pre-wrap overflow-auto max-h-96">
            {detail.defaultBody}
          </pre>
        </div>

        <div className="space-y-2">
          <label className="text-xs uppercase tracking-wide text-muted-foreground">
            Override subject
          </label>
          <form.Field name="subject">
            {(field) => (
              <input
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm font-mono"
                placeholder="(leave empty to inherit the default)"
              />
            )}
          </form.Field>
          <label className="text-xs uppercase tracking-wide text-muted-foreground">
            Override body
          </label>
          <form.Field name="body">
            {(field) => (
              <textarea
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                rows={16}
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono"
                spellCheck={false}
              />
            )}
          </form.Field>
          <div className="flex items-center gap-2 text-sm">
            <form.Field name="enabled">
              {(field) => (
                <input
                  id="enabled"
                  type="checkbox"
                  checked={field.state.value}
                  onChange={(e) => field.handleChange(e.target.checked)}
                  onBlur={field.handleBlur}
                />
              )}
            </form.Field>
            <label htmlFor="enabled" className="text-foreground">
              Enabled (uncheck to keep the override on file but use the default at delivery)
            </label>
          </div>
          <div className="flex items-center gap-2 pt-1">
            <button
              type="button"
              onClick={() => void form.handleSubmit()}
              disabled={saving}
              className="inline-flex items-center gap-1 rounded-md bg-foreground px-3 py-1.5 text-sm font-medium text-background hover:opacity-90 disabled:opacity-50"
            >
              {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
              Save override
            </button>
            <button
              type="button"
              onClick={handleReset}
              disabled={saving || !detail.hasOverride}
              className="inline-flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-sm font-medium hover:bg-muted disabled:opacity-50"
            >
              <RotateCcw className="h-3.5 w-3.5" /> Reset to default
            </button>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="space-y-2">
          <label className="text-xs uppercase tracking-wide text-muted-foreground">
            Variables
          </label>
          <div className="rounded-md border border-border overflow-hidden">
            <Table className="w-full text-xs">
              <TableHeader className="bg-muted/50">
                <TableRow>
                  <TableHead className="px-3 py-1.5 text-left font-medium">Name</TableHead>
                  <TableHead className="px-3 py-1.5 text-left font-medium">Description</TableHead>
                  <TableHead className="px-3 py-1.5 text-left font-medium">Required</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {variableList.map((v) => (
                  <TableRow key={v.name} className="border-t border-border">
                    <TableCell className="px-3 py-1.5 font-mono">{v.name}</TableCell>
                    <TableCell className="px-3 py-1.5 text-muted-foreground">{v.description}</TableCell>
                    <TableCell className="px-3 py-1.5">{v.required ? 'yes' : 'no'}</TableCell>
                  </TableRow>
                ))}
                {variableList.length === 0 && (
                  <TableRow>
                    <TableCell className="px-3 py-3 text-center text-muted-foreground" colSpan={3}>
                      No declared variables.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>
        </div>

        <div className="space-y-2">
          <label className="text-xs uppercase tracking-wide text-muted-foreground">
            Sample variables (JSON)
          </label>
          <form.Field name="samples">
            {(field) => (
              <textarea
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                rows={8}
                spellCheck={false}
                className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono"
              />
            )}
          </form.Field>
          <button
            type="button"
            onClick={handlePreview}
            disabled={previewing}
            className="inline-flex items-center gap-1 rounded-md border border-border px-3 py-1.5 text-sm font-medium hover:bg-muted disabled:opacity-50"
          >
            {previewing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Eye className="h-3.5 w-3.5" />}
            Preview
          </button>
          {previewMissing && previewMissing.length > 0 && (
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
              Missing required variables:{' '}
              {previewMissing.map((m) => (
                <code key={m} className="px-1 mx-0.5 rounded bg-amber-500/20">
                  {m}
                </code>
              ))}
            </div>
          )}
          {previewErr && !previewMissing && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive">
              {previewErr}
            </div>
          )}
          {preview && (
            <div className="space-y-2">
              <div>
                <label className="text-xs uppercase tracking-wide text-muted-foreground">
                  Rendered subject
                </label>
                <pre className="rounded-md border border-border bg-muted/30 p-3 text-xs whitespace-pre-wrap">
                  {preview.subject || <span className="text-muted-foreground">(empty)</span>}
                </pre>
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-muted-foreground">
                  Rendered body
                </label>
                <pre className="rounded-md border border-border bg-muted/30 p-3 text-xs whitespace-pre-wrap max-h-96 overflow-auto">
                  {preview.body}
                </pre>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// seedSamples produces a starter JSON document from the declared
// Variables[] so the operator doesn't stare at an empty textarea. The
// variable names use dotted notation; we unflatten into a nested
// object so the preview engine sees the same shape the dispatchers
// produce.
function seedSamples(detail: NotificationTemplateDetail): string {
  const root: Record<string, unknown> = {};
  for (const v of detail.variables) {
    const parts = v.name.split('.');
    let cur = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const p = parts[i];
      if (typeof cur[p] !== 'object' || cur[p] === null) cur[p] = {};
      cur = cur[p] as Record<string, unknown>;
    }
    cur[parts[parts.length - 1]] = v.example || '';
  }
  return JSON.stringify(root, null, 2);
}

export const Route = createFileRoute('/dashboard/settings/templates/$key/')({
  component: NotificationTemplateEditorPage,
});
