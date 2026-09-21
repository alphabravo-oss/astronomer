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
import { Link as RouterLink } from "@tanstack/react-router";
import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Field } from "@/components/form/fields";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  listReadAuditPolicies,
  createReadAuditPolicy,
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
  const [items, setItems] = useState<ReadAuditPolicyView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [showCreate, setShowCreate] = useState(false);
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
            onClick={() => setShowCreate(true)}
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

      {showCreate && (
        <CreatePolicyModal
          onClose={() => setShowCreate(false)}
          onCreated={async () => {
            setShowCreate(false);
            await refresh();
          }}
        />
      )}
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

function CreatePolicyModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [pathPattern, setPathPattern] = useState("");
  const [verbs, setVerbs] = useState("GET");
  const [sampleRate, setSampleRate] = useState(1);
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await createReadAuditPolicy({
        name,
        description,
        path_pattern: pathPattern,
        verbs,
        sample_rate: sampleRate,
        enabled,
      });
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ModalShell
      title="New read-audit policy"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose} disabled={busy}>
            Cancel
          </ActionButton>
          <ActionButton
            intent="primary"
            onClick={submit}
            disabled={busy || !name || !pathPattern}
            loading={busy}
            loadingLabel="Creating…"
          >
            Create
          </ActionButton>
        </>
      }
    >
      {error && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-sm text-destructive">
          {error}
        </div>
      )}
      <Field label="Name">
        <Input value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Description">
        <Input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
      </Field>
      <Field label="Path pattern (e.g. /admin/sso or /projects/*/cloud-credentials)">
        <Input
          value={pathPattern}
          onChange={(e) => setPathPattern(e.target.value)}
          className="font-mono"
        />
      </Field>
      <Field label="Verbs (comma-separated or *)">
        <Input value={verbs} onChange={(e) => setVerbs(e.target.value)} />
      </Field>
      <Field label={`Sample rate: ${Math.round(sampleRate * 100)}%`}>
        <Input
          type="range"
          min={0}
          max={1}
          step={0.05}
          value={sampleRate}
          onChange={(e) => setSampleRate(Number(e.target.value))}
          className="w-full"
        />
      </Field>
      <label className="flex items-center gap-2 text-sm text-foreground">
        <Input
          type="checkbox"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />
        Enabled
      </label>
    </ModalShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/read-audit/")({
  component: ReadAuditPoliciesPage,
});
