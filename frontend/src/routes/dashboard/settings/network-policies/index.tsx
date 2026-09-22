import { Input } from "@/components/ui/input";
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

import { useState } from "react";
import { useNetworkPolicyTemplates } from "@/lib/hooks/policy-queries";
import { QueryStates } from "@/components/ui/query-states";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  ArrowLeft,
  Plus,
  Trash2,
  Save,
  Copy,
  Loader2,
  ShieldCheck,
} from "lucide-react";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { useAppForm, useStore } from "@/lib/form";
import { Textarea } from "@/components/ui/textarea";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  createNetworkPolicyTemplate,
  updateNetworkPolicyTemplate,
  deleteNetworkPolicyTemplate,
  type NetworkPolicyTemplate,
  type NetworkPolicyTemplateWriteRequest,
} from "@/lib/api/settings";

function KindBadge({ kind }: { kind: "builtin" | "custom" }) {
  const palette =
    kind === "builtin"
      ? "bg-status-info/10 text-status-info border-status-info/30"
      : "bg-status-success/10 text-status-success border-status-success/30";
  return (
    <span
      className={`text-xs px-2 py-0.5 rounded-sm border font-medium uppercase ${palette}`}
    >
      {kind}
    </span>
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
        <div className="text-xs text-muted-foreground font-mono">
          {tmpl.slug}
        </div>
      </TableCell>
      <TableCell className="px-3 py-3 align-top">
        <KindBadge kind={tmpl.kind} />
      </TableCell>
      <TableCell className="px-3 py-3 align-top text-sm text-muted-foreground max-w-md">
        {tmpl.description}
      </TableCell>
      <TableCell className="px-3 py-3 align-top">
        <span
          className={`text-xs px-2 py-0.5 rounded-sm border font-medium ${
            tmpl.enabled
              ? "bg-status-success/10 text-status-success border-status-success/30"
              : "bg-muted text-muted-foreground border-border"
          }`}
        >
          {tmpl.enabled ? "enabled" : "disabled"}
        </span>
      </TableCell>
      <TableCell className="px-3 py-3 align-top text-right">
        <div className="flex items-center justify-end gap-1">
          <button
            type="button"
            className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-sm border border-border hover:bg-muted"
            onClick={onClone}
            title="Create an editable copy"
          >
            <Copy className="h-3 w-3" /> Clone
          </button>
          {tmpl.kind === "custom" && (
            <>
              <button
                type="button"
                className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-sm border border-border hover:bg-muted"
                onClick={onEdit}
              >
                Edit
              </button>
              <button
                type="button"
                className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-sm border border-status-error/30 text-status-error hover:bg-status-error/10"
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
  const templatesQuery = useNetworkPolicyTemplates();
  const templates = templatesQuery.data ?? [];
  const loading = templatesQuery.isLoading;
  const [draft, setDraft] = useState<DraftForm | null>(null);
  const [deleteTarget, setDeleteTarget] =
    useState<NetworkPolicyTemplate | null>(null);
  const [deleting, setDeleting] = useState(false);
  // Remount key for the draft editor: each Clone/Edit/New replaces the whole
  // draft, so the form below re-seeds from scratch exactly like the old
  // setDraft(...) did.
  const [draftNonce, setDraftNonce] = useState(0);

  const openDraft = (next: DraftForm) => {
    setDraft(next);
    setDraftNonce((n) => n + 1);
  };

  const refresh = async () => {
    await templatesQuery.refetch();
  };

  const handleClone = (tmpl: NetworkPolicyTemplate) => {
    openDraft({
      clone_from: tmpl.slug,
      slug: `${tmpl.slug}_copy`,
      name: `${tmpl.name} (copy)`,
      description: tmpl.description,
      spec_template: tmpl.spec_template,
      enabled: true,
    });
  };

  const handleEdit = (tmpl: NetworkPolicyTemplate) => {
    openDraft({
      id: tmpl.id,
      slug: tmpl.slug,
      name: tmpl.name,
      description: tmpl.description,
      spec_template: tmpl.spec_template,
      enabled: tmpl.enabled,
    });
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteNetworkPolicyTemplate(deleteTarget.id);
      toastSuccess("Template deleted");
      setDeleteTarget(null);
      await refresh();
    } catch (err: unknown) {
      toastApiError("Delete failed", err);
    } finally {
      setDeleting(false);
    }
  };

  if (templatesQuery.isError)
    return <QueryStates query={templatesQuery}>{null}</QueryStates>;

  return (
    <PageShell className="space-y-4">
      <RouterLink
        to="/dashboard/settings"
        className="inline-flex items-center text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4 mr-1" /> Back to settings
      </RouterLink>
      <PageHeader
        title={
          <span className="flex items-center gap-2">
            <ShieldCheck className="h-5 w-5" /> Network policy templates
          </span>
        }
        description="Pre-built Kubernetes NetworkPolicy bundles. Built-in rows are read-only — clone to create an editable custom row. Apply templates to namespaces from the cluster detail page's Network policies tab."
        actions={
          <button
            type="button"
            onClick={() =>
              openDraft({
                slug: "",
                name: "",
                description: "",
                spec_template:
                  "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: {{.PolicyName}}\n  namespace: {{.Namespace}}\nspec:\n  podSelector: {}\n  policyTypes: [Ingress]\n",
                enabled: true,
              })
            }
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-sm border border-border bg-card hover:bg-muted"
          >
            <Plus className="h-4 w-4" /> New custom template
          </button>
        }
      />

      {loading ? (
        <div className="flex items-center text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 mr-2 animate-spin" /> Loading templates...
        </div>
      ) : (
        <div
          className="overflow-x-auto rounded-lg border border-border"
          role="region"
          aria-label="Network policies"
          tabIndex={0}
        >
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted">
              <TableRow className="text-left">
                <TableHead className="px-3 py-2 font-medium">
                  Template
                </TableHead>
                <TableHead className="px-3 py-2 font-medium">Kind</TableHead>
                <TableHead className="px-3 py-2 font-medium">
                  Description
                </TableHead>
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
                  onDelete={() => setDeleteTarget(t)}
                />
              ))}
              {templates.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={5}
                    className="px-3 py-6 text-center text-sm text-muted-foreground"
                  >
                    No templates. Run migration 068 to seed the built-ins.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}

      {draft && (
        <TemplateDraftForm
          key={draftNonce}
          draft={draft}
          onCancel={() => setDraft(null)}
          onSaved={async () => {
            setDraft(null);
            await refresh();
          }}
        />
      )}
      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void handleDelete()}
        title="Delete network policy template"
        description="This permanently removes the custom template from the catalog."
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deleting}
        impact={
          deleteTarget
            ? {
                scope: deleteTarget.name,
                consequences: [
                  "The template can no longer be applied to namespaces.",
                  "Existing applications are not automatically revoked.",
                ],
                recovery:
                  "Recreate the custom template from its YAML definition.",
              }
            : undefined
        }
      />
    </PageShell>
  );
}

function TemplateDraftForm({
  draft,
  onCancel,
  onSaved,
}: {
  draft: DraftForm;
  onCancel: () => void;
  onSaved: () => Promise<void>;
}) {
  const [saveError, setSaveError] = useState<unknown>(null);
  const form = useAppForm({
    defaultValues: {
      slug: draft.slug ?? "",
      name: draft.name,
      description: draft.description ?? "",
      spec_template: draft.spec_template,
      enabled: draft.enabled ?? true,
    },
    onSubmit: async ({ value }) => {
      setSaveError(null);
      try {
        if (draft.id) {
          await updateNetworkPolicyTemplate(draft.id, {
            name: value.name,
            description: value.description,
            spec_template: value.spec_template,
            enabled: value.enabled,
          });
          toastSuccess("Template updated");
        } else {
          await createNetworkPolicyTemplate({
            ...draft,
            slug: value.slug,
            name: value.name,
            description: value.description,
            spec_template: value.spec_template,
            enabled: value.enabled,
          });
          toastSuccess("Template created");
        }
        await onSaved();
      } catch (err: unknown) {
        setSaveError(err);
        toastApiError("Save failed", err);
      }
    },
  });
  const saving = useStore(form.store, (s) => s.isSubmitting);

  return (
    <div className="rounded-lg border border-border bg-card p-4 space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-base font-semibold">
          {draft.id ? "Edit template" : "New template"}
        </h2>
        <button
          type="button"
          className="text-xs text-muted-foreground hover:text-foreground"
          onClick={onCancel}
        >
          Cancel
        </button>
      </div>
      <form.AppForm>
        <form.FormErrorSummary
          serverError={saveError ? extractApiErrorMessage(saveError) : null}
        />
      </form.AppForm>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
        <label className="text-sm space-y-1">
          <span className="text-muted-foreground">Slug</span>
          <form.Field name="slug">
            {(field) => (
              <Input
                type="text"
                className="w-full px-2 py-1 rounded-sm border border-border bg-background font-mono text-sm"
                value={field.state.value}
                disabled={!!draft.id}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="my_custom_policy"
              />
            )}
          </form.Field>
        </label>
        <label className="text-sm space-y-1">
          <span className="text-muted-foreground">Name</span>
          <form.Field name="name">
            {(field) => (
              <Input
                type="text"
                className="w-full px-2 py-1 rounded-sm border border-border bg-background text-sm"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
        </label>
      </div>
      <label className="text-sm space-y-1 block">
        <span className="text-muted-foreground">Description</span>
        <form.Field name="description">
          {(field) => (
            <Textarea
              className="w-full px-2 py-1 rounded-sm border border-border bg-background text-sm"
              rows={2}
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            />
          )}
        </form.Field>
      </label>
      <label className="text-sm space-y-1 block">
        <span className="text-muted-foreground">
          Spec template (Go text/template + YAML)
        </span>
        <form.Field name="spec_template">
          {(field) => (
            <Textarea
              className="w-full px-2 py-1 rounded-sm border border-border bg-background text-xs font-mono"
              rows={14}
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            />
          )}
        </form.Field>
        <span className="text-xs text-muted-foreground">
          Variables: <code className="font-mono">{"{{.Namespace}}"}</code>,{" "}
          <code className="font-mono">{"{{.Project}}"}</code>,{" "}
          <code className="font-mono">{"{{.PolicyName}}"}</code>
        </span>
      </label>
      <label className="inline-flex items-center gap-2 text-sm">
        <form.Field name="enabled">
          {(field) => (
            <Input
              type="checkbox"
              checked={field.state.value}
              onChange={(e) => field.handleChange(e.target.checked)}
              onBlur={field.handleBlur}
            />
          )}
        </form.Field>
        Enabled
      </label>
      <div>
        <button
          type="button"
          onClick={() => void form.handleSubmit()}
          disabled={saving}
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm rounded-sm border border-border bg-foreground text-background hover:opacity-90 disabled:opacity-50"
        >
          {saving ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <Save className="h-4 w-4" />
          )}
          Save
        </button>
      </div>
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

export const Route = createFileRoute("/dashboard/settings/network-policies/")({
  component: NetworkPoliciesSettingsPage,
});
