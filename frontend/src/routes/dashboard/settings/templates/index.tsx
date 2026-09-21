import { createFileRoute } from "@tanstack/react-router";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
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
import { Link as RouterLink } from "@tanstack/react-router";
import { ArrowLeft, FileText } from "lucide-react";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { PageHeader, PageShell } from "@/components/ui/page";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { useNotificationTemplates } from "@/components/settings/hooks";

function NotificationTemplatesPage() {
  return (
    <SettingsAuthGate>
      <NotificationTemplatesList />
    </SettingsAuthGate>
  );
}

function NotificationTemplatesList() {
  const templatesQuery = useNotificationTemplates();

  return (
    <PageShell>
      <RouterLink
        to="/dashboard/settings"
        className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
      >
        <ArrowLeft className="h-4 w-4" /> Settings
      </RouterLink>
      <PageHeader
        title="Notification templates"
        description="Customize the subject and body of every transactional email and webhook payload. Built-in defaults apply when no override is saved."
      />

      <QueryStates
        query={templatesQuery}
        loadingTitle="Loading notification templates"
        permission="settings:read"
        errorTitle="Failed to load notification templates"
        isEmpty={(items) => items.length === 0}
        empty={
          <EmptyState
            icon={FileText}
            title="No notification templates registered"
            description="The server has not registered any email or webhook templates. Check the notification registry configuration."
            // terminal: templates come from the server-side registry, not a UI action.
            terminal
          />
        }
      >
        {(items) => (
          <div className="rounded-lg border border-border overflow-hidden">
            <Table className="w-full text-sm">
              <TableHeader className="bg-muted/50 text-left text-xs uppercase tracking-wide text-muted-foreground">
                <TableRow>
                  <TableHead className="px-4 py-2 font-medium">Key</TableHead>
                  <TableHead className="px-4 py-2 font-medium">
                    Channel
                  </TableHead>
                  <TableHead className="px-4 py-2 font-medium">
                    Description
                  </TableHead>
                  <TableHead className="px-4 py-2 font-medium">
                    Override
                  </TableHead>
                  <TableHead className="px-4 py-2 font-medium" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {(items ?? []).map((t) => (
                  <TableRow
                    key={t.key}
                    className="border-t border-border hover:bg-muted/30"
                  >
                    <TableCell className="px-4 py-2 font-mono text-xs">
                      {t.key}
                    </TableCell>
                    <TableCell className="px-4 py-2">
                      <span className="text-xs px-2 py-0.5 rounded-md bg-muted text-foreground">
                        {t.channel}
                      </span>
                    </TableCell>
                    <TableCell className="px-4 py-2 text-muted-foreground">
                      {t.description}
                    </TableCell>
                    <TableCell className="px-4 py-2">
                      {t.hasOverride ? (
                        <span
                          className={`text-xs px-2 py-0.5 rounded-md ${
                            t.enabled
                              ? "bg-status-success/15 text-status-success"
                              : "bg-status-warning/15 text-status-warning"
                          }`}
                        >
                          {t.enabled ? "enabled" : "disabled"}
                        </span>
                      ) : (
                        <span className="text-xs text-muted-foreground">
                          default
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="px-4 py-2 text-right">
                      <RouterLink
                        to="/dashboard/settings/templates/$key" params={{ key: t.key }}
                        className="text-sm font-medium text-foreground hover:underline"
                      >
                        Edit
                      </RouterLink>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </QueryStates>
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/templates/")({
  component: NotificationTemplatesPage,
});
