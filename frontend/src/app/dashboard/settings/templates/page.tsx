'use client';

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
import Link from 'next/link';
import { ArrowLeft, FileText, Loader2 } from 'lucide-react';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import {
  listNotificationTemplates,
  type NotificationTemplateListItem,
} from '@/lib/api/settings';

export default function NotificationTemplatesPage() {
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
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <Link
          href="/dashboard/settings"
          className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
        >
          <ArrowLeft className="h-4 w-4" /> Settings
        </Link>
      </div>
      <div className="flex items-start gap-3">
        <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <FileText className="h-5 w-5 text-foreground" />
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">
            Notification templates
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            Customize the subject and body of every transactional email and webhook payload.
            Built-in defaults apply when no override is saved.
          </p>
        </div>
      </div>

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
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">Key</th>
                <th className="px-4 py-2 font-medium">Channel</th>
                <th className="px-4 py-2 font-medium">Description</th>
                <th className="px-4 py-2 font-medium">Override</th>
                <th className="px-4 py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {(items ?? []).map((t) => (
                <tr key={t.key} className="border-t border-border hover:bg-muted/30">
                  <td className="px-4 py-2 font-mono text-xs">{t.key}</td>
                  <td className="px-4 py-2">
                    <span className="text-xs px-2 py-0.5 rounded-md bg-muted text-foreground">
                      {t.channel}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">{t.description}</td>
                  <td className="px-4 py-2">
                    {t.hasOverride ? (
                      <span
                        className={`text-xs px-2 py-0.5 rounded-md ${
                          t.enabled
                            ? 'bg-emerald-500/15 text-emerald-600'
                            : 'bg-amber-500/15 text-amber-600'
                        }`}
                      >
                        {t.enabled ? 'enabled' : 'disabled'}
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">default</span>
                    )}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <Link
                      href={`/dashboard/settings/templates/${encodeURIComponent(t.key)}`}
                      className="text-sm font-medium text-foreground hover:underline"
                    >
                      Edit
                    </Link>
                  </td>
                </tr>
              ))}
              {items && items.length === 0 && (
                <tr>
                  <td className="px-4 py-6 text-center text-muted-foreground" colSpan={5}>
                    No templates registered.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
