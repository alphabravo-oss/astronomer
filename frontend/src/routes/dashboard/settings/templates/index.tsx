import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/templates — list of every notification template
 * registered in the Go `internal/notify` registry. Operators see a
 * table grouped by channel (email/webhook) with a badge for whether a
 * tenant override is currently in effect.
 *
 * Migration 059 backs this surface. The list endpoint is superuser-
 * gated; `SettingsAuthGate` renders the same 403 placeholder the
 * other settings subpages use.
 */
import { useEffect, useState } from 'react';
import { Link } from '@/lib/link';
import { ArrowLeft, Loader2 } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { PageHeader, PageShell } from '@/components/ui/page';
import {
  listNotificationTemplates,
  type NotificationTemplateListItem,
} from '@/lib/api/settings';

function NotificationTemplatesPage() {
  return (
    <SettingsAuthGate>
      <NotificationTemplatesList />
    </SettingsAuthGate>
  );
}

function NotificationTemplatesList() {
  const [items, setItems] = useState<NotificationTemplateListItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const data = await listNotificationTemplates();
        if (!cancelled) setItems(data);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <PageShell>
      <Link
        href="/dashboard/settings"
        className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
      >
        <ArrowLeft className="h-4 w-4" /> Settings
      </Link>
      <PageHeader
        title="Notification templates"
        description="Customize the subject and body of every transactional email and webhook payload. Built-in defaults apply when no override is saved."
      />

      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}

      {items === null && !error ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading templates…
        </div>
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <TableRow>
                <TableHead className="px-4 py-2 font-medium">Key</TableHead>
                <TableHead className="px-4 py-2 font-medium">Channel</TableHead>
                <TableHead className="px-4 py-2 font-medium">Description</TableHead>
                <TableHead className="px-4 py-2 font-medium">Override</TableHead>
                <TableHead className="px-4 py-2 font-medium" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {(items ?? []).map((t) => (
                <TableRow key={t.key} className="border-t border-border hover:bg-muted/30">
                  <TableCell className="px-4 py-2 font-mono text-xs">{t.key}</TableCell>
                  <TableCell className="px-4 py-2">
                    <span className="text-xs px-2 py-0.5 rounded-md bg-muted text-foreground">
                      {t.channel}
                    </span>
                  </TableCell>
                  <TableCell className="px-4 py-2 text-muted-foreground">{t.description}</TableCell>
                  <TableCell className="px-4 py-2">
                    {t.hasOverride ? (
                      <span
                        className={`text-xs px-2 py-0.5 rounded-md ${
                          t.enabled
                            ? 'bg-status-success/15 text-status-success'
                            : 'bg-status-warning/15 text-status-warning'
                        }`}
                      >
                        {t.enabled ? 'enabled' : 'disabled'}
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">default</span>
                    )}
                  </TableCell>
                  <TableCell className="px-4 py-2 text-right">
                    <Link
                      href={`/dashboard/settings/templates/${encodeURIComponent(t.key)}`}
                      className="text-sm font-medium text-foreground hover:underline"
                    >
                      Edit
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
              {items && items.length === 0 && (
                <TableRow>
                  <TableCell className="px-4 py-6 text-center text-muted-foreground" colSpan={5}>
                    No templates registered.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </PageShell>
  );
}

export const Route = createFileRoute('/dashboard/settings/templates/')({
  component: NotificationTemplatesPage,
});
