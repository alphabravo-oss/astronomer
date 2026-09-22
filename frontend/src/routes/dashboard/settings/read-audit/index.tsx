import { createFileRoute } from "@tanstack/react-router";
import { DataTable, type Column } from "@/components/ui/data-table";
/**
 * /dashboard/settings/read-audit — operator UI for the read-side audit
 * policies (migration 063). Each row is a path-prefix + verbs +
 * sample-rate combination that, when matched, fires the read auditor.
 * Default seeds (cloud creds, registry creds, SSO, webhooks, SIEM,
 * audit log, support bundle, admin settings) ship enabled; operators
 * can add their own.
 *
 * Backend: /api/v1/admin/read-audit-policies/. Superuser-gated.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  listReadAuditPolicies,
  updateReadAuditPolicy,
  deleteReadAuditPolicy,
  type ReadAuditPolicyView,
} from "@/lib/api/settings";

function ReadAuditPoliciesPage() {
  return (
    <SettingsAuthGate>
      <ReadAuditPoliciesList />
    </SettingsAuthGate>
  );
}

function ReadAuditPoliciesList() {
  const navigate = useNavigate();
  const [items, setItems] = useState<ReadAuditPolicyView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ReadAuditPolicyView | null>(
    null,
  );

  async function refresh(signal?: AbortSignal) {
    try {
      const data = await listReadAuditPolicies({ signal });
      setItems(data);
    } catch (err) {
      if (signal?.aborted) return;
      setError(err instanceof Error ? err.message : "Failed to load");
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    (async () => {
      try {
        const data = await listReadAuditPolicies({ signal: controller.signal });
        if (!controller.signal.aborted) setItems(data);
      } catch (err) {
        if (!controller.signal.aborted)
          setError(err instanceof Error ? err.message : "Failed to load");
      }
    })();
    return () => {
      controller.abort();
    };
  }, []);

  const toggleEnabled = useCallback(async (p: ReadAuditPolicyView) => {
    setBusyId(p.id);
    try {
      await updateReadAuditPolicy(p.id, { enabled: !p.enabled });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    } finally {
      setBusyId(null);
    }
  }, []);

  async function remove() {
    if (!deleteTarget) return;
    setBusyId(deleteTarget.id);
    try {
      await deleteReadAuditPolicy(deleteTarget.id);
      setDeleteTarget(null);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusyId(null);
    }
  }

  const columns = useMemo<Column<ReadAuditPolicyView>[]>(
    () => [
      {
        key: "name",
        header: "Name",
        accessor: (p) => <span className="font-mono text-xs">{p.name}</span>,
        searchAccessor: (p) => p.name,
        sortAccessor: (p) => p.name,
      },
      {
        key: "path_pattern",
        header: "Path pattern",
        accessor: (p) => (
          <span className="font-mono text-xs">{p.path_pattern}</span>
        ),
        searchAccessor: (p) => p.path_pattern,
        sortAccessor: (p) => p.path_pattern,
      },
      {
        key: "verbs",
        header: "Verbs",
        accessor: (p) => <span className="text-xs">{p.verbs}</span>,
        searchAccessor: (p) => p.verbs,
        sortAccessor: (p) => p.verbs,
        width: "8rem",
      },
      {
        key: "sample_rate",
        header: "Sample",
        accessor: (p) => (
          <span className="text-xs">{Math.round(p.sample_rate * 100)}%</span>
        ),
        sortAccessor: (p) => p.sample_rate,
        align: "right",
        width: "6rem",
      },
      {
        key: "enabled",
        header: "Enabled",
        accessor: (p) => (
          <button
            disabled={busyId === p.id}
            onClick={() => toggleEnabled(p)}
            className={`text-xs px-2 py-0.5 rounded-md ${
              p.enabled
                ? "bg-status-success/15 text-status-success"
                : "bg-status-warning/15 text-status-warning"
            }`}
          >
            {p.enabled ? "enabled" : "disabled"}
          </button>
        ),
        searchAccessor: (p) => (p.enabled ? "enabled" : "disabled"),
        sortAccessor: (p) => (p.enabled ? 1 : 0),
        filter: { label: "Enabled" },
        width: "8rem",
      },
      {
        key: "actions",
        header: "",
        hideable: false,
        accessor: (p) => (
          <button
            disabled={busyId === p.id}
            onClick={() => setDeleteTarget(p)}
            className="text-muted-foreground hover:text-destructive"
            title="Delete policy"
          >
            <Trash2 className="h-4 w-4" />
          </button>
        ),
        align: "right",
        width: "4rem",
      },
    ],
    [busyId, toggleEnabled],
  );

  return (
    <PageShell>
      <RouterLink
        to="/dashboard/settings"
        className="text-sm text-muted-foreground hover:text-foreground inline-flex items-center gap-1"
      >
        <ArrowLeft className="h-4 w-4" /> Settings
      </RouterLink>
      <PageHeader
        title="Read-side audit policies"
        description='Configure which GET endpoints emit an audit row. HIPAA / PCI compliance requires "who saw what credential and when" — the seeded policies cover cloud credentials, registry secrets, SSO, webhooks, SIEM auth, the audit log itself, support bundles, and admin settings.'
        actions={
          <ActionButton
            icon={<Plus className="h-4 w-4" />}
            onClick={() =>
              void navigate({ to: "/dashboard/settings/read-audit/new" })
            }
          >
            New policy
          </ActionButton>
        }
      />

      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}

      <DataTable
        data={items ?? []}
        columns={columns}
        keyExtractor={(p) => p.id}
        density="compact"
        loading={items === null && !error}
        searchPlaceholder="Search policies..."
        emptyState={{
          title: "No policies configured",
          description: "Read-side audit is currently disabled.",
        }}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void remove()}
        title="Delete read-audit policy"
        description="This permanently removes the rule that records matching read requests."
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={busyId === deleteTarget?.id}
        impact={
          deleteTarget
            ? {
                scope: deleteTarget.name,
                consequences: [
                  `Requests matching ${deleteTarget.path_pattern} will no longer be sampled by this policy.`,
                  "Compliance evidence coverage may be reduced immediately.",
                ],
                recovery: "Create an equivalent read-audit policy.",
              }
            : undefined
        }
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/read-audit/")({
  component: ReadAuditPoliciesPage,
});
