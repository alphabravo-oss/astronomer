import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
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
import { useState } from 'react';
import { useParams } from '@/lib/navigation';
import { Plus, Loader2, Trash2, Link2 } from 'lucide-react';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import {
  useProjectCatalogs,
  useCreateProjectCatalog,
  useSubscribeProjectCatalog,
  useDeleteProjectCatalog,
  canEditProject,
} from '@/components/projects/hooks';
import { useCurrentUser } from '@/lib/hooks';

import type { ProjectCatalog } from '@/lib/api/project-detail';
import { cn, formatRelativeTime } from '@/lib/utils';

function ProjectCatalogsPage() {
  const params = useParams();
  const projectId = params.id as string;
  const { data: user } = useCurrentUser();
  const canEdit = canEditProject(user);

  const { data: catalogs = [], isLoading } = useProjectCatalogs(projectId);
  const createMutation = useCreateProjectCatalog(projectId);
  const subscribeMutation = useSubscribeProjectCatalog(projectId);
  const deleteMutation = useDeleteProjectCatalog(projectId);

  const [showAdd, setShowAdd] = useState(false);
  const [form, setForm] = useState({ name: '', url: '', description: '' });

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await createMutation.mutateAsync({
        name: form.name,
        url: form.url,
        description: form.description,
      });
      setShowAdd(false);
      setForm({ name: '', url: '', description: '' });
    } catch {
      // Toast handled in the hook.
    }
  };

  const handleSubscribe = (cat: ProjectCatalog) => {
    subscribeMutation.mutate(cat.id);
  };

  const handleUnsubscribe = (cat: ProjectCatalog) => {
    const isOwned = cat.visibility === 'own';
    const msg = isOwned
      ? `Delete the project-owned catalog "${cat.name}"? This removes the catalog and all of its charts; no other project can use it.`
      : `Unsubscribe from catalog "${cat.name}"? The catalog itself stays available to other projects.`;
    if (!confirm(msg)) return;
    deleteMutation.mutate(cat.id);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          Helm chart catalogs available to this project. Globals are shared
          across all projects; private catalogs are scoped to this project
          only.
        </p>
        {canEdit && (
          <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setShowAdd(true)}>
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
                <TableHead className="text-left font-medium py-2 px-3">Name</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">URL</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Visibility</TableHead>
                <TableHead className="text-left font-medium py-2 px-3">Last sync</TableHead>
                <TableHead className="text-right font-medium py-2 px-3">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {catalogs.map((cat) => (
                <TableRow key={cat.id} className="border-b border-border last:border-0">
                  <TableCell className="py-2 px-3">
                    <div className="font-medium text-foreground">{cat.name}</div>
                    {cat.description ? (
                      <div className="text-xs text-muted-foreground">{cat.description}</div>
                    ) : null}
                  </TableCell>
                  <TableCell className="py-2 px-3 text-xs font-mono text-muted-foreground">{cat.url}</TableCell>
                  <TableCell className="py-2 px-3">
                    <VisibilityBadge visibility={cat.visibility} />
                  </TableCell>
                  <TableCell className="py-2 px-3 text-xs text-muted-foreground">
                    {cat.lastSyncedAt ? formatRelativeTime(cat.lastSyncedAt) : 'never'}
                  </TableCell>
                  <TableCell className="py-2 px-3 text-right">
                    {canEdit && cat.visibility === 'public' && (
                      <button
                        onClick={() => handleSubscribe(cat)}
                        className="inline-flex items-center gap-1 text-xs text-foreground hover:opacity-80"
                      >
                        <Link2 className="h-3.5 w-3.5" />
                        Subscribe
                      </button>
                    )}
                    {canEdit && cat.visibility !== 'public' && (
                      <button
                        onClick={() => handleUnsubscribe(cat)}
                        className="inline-flex items-center gap-1 text-xs text-destructive hover:opacity-80"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        {cat.visibility === 'own' ? 'Delete' : 'Unsubscribe'}
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
        <form onSubmit={handleAdd}>
          <ModalShell
            title="Add private catalog"
            subtitle="Subscribes this project to a Helm chart repository. Only this project can see private catalogs."
            onClose={() => setShowAdd(false)}
            size="sm"
            footerClassName="flex items-center justify-end gap-2"
            footer={
              <>
                <ActionButton type="button" intent="ghost" onClick={() => setShowAdd(false)}>
                  Cancel
                </ActionButton>
                <ActionButton type="submit" intent="primary" loading={createMutation.isPending} loadingLabel="Creating…">
                  Create
                </ActionButton>
              </>
            }
          >
            <div className="space-y-1">
              <label className="text-xs font-medium text-foreground">Name</label>
              <Input
                type="text"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                required
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs font-medium text-foreground">Repository URL</label>
              <Input
                type="url"
                value={form.url}
                onChange={(e) => setForm({ ...form, url: e.target.value })}
                placeholder="https://charts.example.com/repo"
                required
                className="font-mono"
              />
            </div>
            <div className="space-y-1">
              <label className="text-xs font-medium text-foreground">Description (optional)</label>
              <Input
                type="text"
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
              />
            </div>
          </ModalShell>
        </form>
      )}
    </div>
  );
}

function VisibilityBadge({ visibility }: { visibility: ProjectCatalog['visibility'] }) {
  const text =
    visibility === 'own'
      ? 'Private'
      : visibility === 'subscribed_public'
        ? 'Subscribed'
        : visibility === 'foreign_private'
          ? 'Foreign'
          : 'Global';
  const tone =
    visibility === 'own'
      ? 'bg-status-info/10 text-status-info'
      : visibility === 'subscribed_public'
        ? 'bg-status-success/10 text-status-success'
        : visibility === 'foreign_private'
          ? 'bg-status-error/10 text-status-error'
          : 'bg-muted text-muted-foreground';
  return (
    <span className={cn('inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium', tone)}>
      {text}
    </span>
  );
}

export const Route = createFileRoute('/dashboard/projects/$id/catalogs/')({
  component: ProjectCatalogsPage,
});
