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
 * Project · Catalogs tab (migration 061 / sprint 16 — BYO Helm catalogs).
 *
 * Three operations on this page:
 *
 *   1. Browse the catalogs visible to this project — globals + own +
 *      subscribed — with a visibility badge per row.
 *   2. "Add private catalog" modal (POST .../catalogs/) that creates a
 *      project-owned catalog. Auto-subscribed on success.
 *   3. "Subscribe" / "Unsubscribe" buttons against each row. The DELETE
 *      semantics are bifurcated server-side (own → delete row,
 *      subscribed → delete subscription) — the UI just tells the user
 *      what's about to happen via a confirm dialog.
 *
 * Mirrors the cloud-credentials list page shape so the project-detail
 * tabs stay visually consistent.
 */
import { useState } from "react";

import { Plus, Loader2, Trash2, Link2 } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { FormShell } from "@/components/ui/form-shell";
import { ModalShell } from "@/components/ui/modal-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  useProjectCatalogs,
  useCreateProjectCatalog,
  useSubscribeProjectCatalog,
  useDeleteProjectCatalog,
  canEditProject,
} from "@/components/projects/hooks";
import { useCurrentUser } from "@/lib/hooks/auth";

import type { ProjectCatalog } from "@/lib/api/project-detail";
import { cn, formatRelativeTime } from "@/lib/utils";

function ProjectCatalogsPage() {
  const params = Route.useParams();
  const projectId = params.id;
  const { data: user } = useCurrentUser();
  const canEdit = canEditProject(user);

  const { data: catalogs = [], isLoading } = useProjectCatalogs(projectId);
  const createMutation = useCreateProjectCatalog(projectId);
  const subscribeMutation = useSubscribeProjectCatalog(projectId);
  const deleteMutation = useDeleteProjectCatalog(projectId);

  const [showAdd, setShowAdd] = useState(false);
  const [removeTarget, setRemoveTarget] = useState<ProjectCatalog | null>(null);
  const [form, setForm] = useState({ name: "", url: "", description: "" });

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await createMutation.mutateAsync({
        name: form.name,
        url: form.url,
        description: form.description,
      });
      setShowAdd(false);
      setForm({ name: "", url: "", description: "" });
    } catch {
      // Toast handled in the hook.
    }
  };

  const handleSubscribe = (cat: ProjectCatalog) => {
    subscribeMutation.mutate(cat.id);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          Helm chart catalogs available to this project. Globals are shared
          across all projects; private catalogs are scoped to this project only.
        </p>
        {canEdit && (
          <ActionButton
            intent="primary"
            icon={<Plus className="h-4 w-4" />}
            onClick={() => setShowAdd(true)}
          >
            Add private catalog
          </ActionButton>
        )}
      </div>

      {isLoading ? (
        <div className="flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : catalogs.length === 0 ? (
        <div className="rounded-xl border border-border bg-card p-8 text-center space-y-2">
          <p className="text-sm text-foreground">No catalogs available.</p>
          <p className="text-xs text-muted-foreground">
            Add a private catalog or have an administrator publish a global one.
          </p>
        </div>
      ) : (
        <div className="rounded-xl border border-border bg-card overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader>
              <TableRow className="text-xs text-muted-foreground border-b border-border bg-muted/30">
                <TableHead className="text-left font-medium py-2 px-3">
                  Name
                </TableHead>
                <TableHead className="text-left font-medium py-2 px-3">
                  URL
                </TableHead>
                <TableHead className="text-left font-medium py-2 px-3">
                  Visibility
                </TableHead>
                <TableHead className="text-left font-medium py-2 px-3">
                  Last sync
                </TableHead>
                <TableHead className="text-right font-medium py-2 px-3">
                  Actions
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {catalogs.map((cat) => (
                <TableRow
                  key={cat.id}
                  className="border-b border-border last:border-0"
                >
                  <TableCell className="py-2 px-3">
                    <div className="font-medium text-foreground">
                      {cat.name}
                    </div>
                    {cat.description ? (
                      <div className="text-xs text-muted-foreground">
                        {cat.description}
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className="py-2 px-3 text-xs font-mono text-muted-foreground">
                    {cat.url}
                  </TableCell>
                  <TableCell className="py-2 px-3">
                    <VisibilityBadge visibility={cat.visibility} />
                  </TableCell>
                  <TableCell className="py-2 px-3 text-xs text-muted-foreground">
                    {cat.lastSyncedAt
                      ? formatRelativeTime(cat.lastSyncedAt)
                      : "never"}
                  </TableCell>
                  <TableCell className="py-2 px-3 text-right">
                    {canEdit && cat.visibility === "public" && (
                      <button
                        onClick={() => handleSubscribe(cat)}
                        className="inline-flex items-center gap-1 text-xs text-foreground hover:opacity-80"
                      >
                        <Link2 className="h-3.5 w-3.5" />
                        Subscribe
                      </button>
                    )}
                    {canEdit && cat.visibility !== "public" && (
                      <button
                        onClick={() => setRemoveTarget(cat)}
                        className="inline-flex items-center gap-1 text-xs text-destructive hover:opacity-80"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        {cat.visibility === "own" ? "Delete" : "Unsubscribe"}
                      </button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {showAdd && (
        <FormShell onSubmit={handleAdd}>
          <ModalShell
            title="Add private catalog"
            subtitle="Subscribes this project to a Helm chart repository. Only this project can see private catalogs."
            onClose={() => setShowAdd(false)}
            size="sm"
            footerClassName="flex items-center justify-end gap-2"
            footer={
              <>
                <ActionButton
                  type="button"
                  intent="ghost"
                  onClick={() => setShowAdd(false)}
                >
                  Cancel
                </ActionButton>
                <ActionButton
                  type="submit"
                  intent="primary"
                  loading={createMutation.isPending}
                  loadingLabel="Creating…"
                >
                  Create
                </ActionButton>
              </>
            }
          >
            <div className="space-y-1">
              <label
                className="text-xs font-medium text-foreground"
                htmlFor="field-9340992c-181"
              >
                Name
              </label>
              <Input
                id="field-9340992c-181"
                type="text"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                required
              />
            </div>
            <div className="space-y-1">
              <label
                className="text-xs font-medium text-foreground"
                htmlFor="field-9340992c-190"
              >
                Repository URL
              </label>
              <Input
                id="field-9340992c-190"
                type="url"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
                placeholder="https://charts.example.com/repo"
                required
                className="font-mono"
              />
            </div>
            <div className="space-y-1">
              <label
                className="text-xs font-medium text-foreground"
                htmlFor="field-9340992c-201"
              >
                Description (optional)
              </label>
              <Input
                id="field-9340992c-201"
                type="text"
                value={form.description}
                onChange={(e) =>
                  setForm({ ...form, description: e.target.value })
                }
              />
            </div>
          </ModalShell>
        </FormShell>
      )}
      <ConfirmDialog
        open={removeTarget !== null}
        onClose={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (!removeTarget) return;
          deleteMutation.mutate(removeTarget.id, {
            onSuccess: () => setRemoveTarget(null),
          });
        }}
        title={
          removeTarget?.visibility === "own"
            ? "Delete private catalog"
            : "Unsubscribe from catalog"
        }
        description={
          removeTarget?.visibility === "own"
            ? "This permanently removes the project-owned catalog."
            : "This removes the catalog from this project only."
        }
        confirmText={
          removeTarget?.visibility === "own" ? "Delete" : "Unsubscribe"
        }
        confirmValue={
          removeTarget?.visibility === "own" ? removeTarget.name : undefined
        }
        variant="destructive"
        loading={deleteMutation.isPending}
        impact={
          removeTarget
            ? removeTarget.visibility === "own"
              ? {
                  scope: removeTarget.name,
                  consequences: [
                    "The catalog and its indexed charts will be removed from this project.",
                    "No other project will be able to use this private catalog.",
                  ],
                  recovery: "Add and sync the private catalog again.",
                }
              : {
                  scope: `${removeTarget.name} subscription for this project`,
                  consequences: [
                    "The catalog's charts will no longer be available in this project.",
                    "The shared catalog remains available to other projects.",
                  ],
                  recovery: "Subscribe this project to the catalog again.",
                }
            : undefined
        }
      />
    </div>
  );
}

function VisibilityBadge({
  visibility,
}: {
  visibility: ProjectCatalog["visibility"];
}) {
  const text =
    visibility === "own"
      ? "Private"
      : visibility === "subscribed_public"
        ? "Subscribed"
        : visibility === "foreign_private"
          ? "Foreign"
          : "Global";
  const tone =
    visibility === "own"
      ? "bg-status-info/10 text-status-info"
      : visibility === "subscribed_public"
        ? "bg-status-success/10 text-status-success"
        : visibility === "foreign_private"
          ? "bg-status-error/10 text-status-error"
          : "bg-muted text-muted-foreground";
  return (
    <span
      className={cn(
        "inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium",
        tone,
      )}
    >
      {text}
    </span>
  );
}

export const Route = createFileRoute("/dashboard/projects/$id/catalogs/")({
  component: ProjectCatalogsPage,
});
