/**
 * Project · namespace assignment.
 *
 * This is the authoring surface for project tenancy. A project's namespaces are
 * not cosmetic: a `project-owner` / `project-member` binding resolves to one
 * namespace-scoped cluster binding per assigned namespace, so this list is
 * exactly what those members can see and do on the cluster. Assigning nothing
 * means the project's role bindings grant nothing.
 *
 * Backed by `POST /projects/{id}/add-namespace/` and `/remove-namespace/`, which
 * write the namespaces JSONB and the `project_namespaces` sidecar in one
 * transaction and flush the RBAC cache.
 *
 * Editing is gated on `clusters:update` (`canAssignProjectNamespaces`), NOT on
 * `projects:update`: assigning a namespace widens what the project's own members
 * can reach, so the server requires authority over the cluster rather than over
 * the project (ProjectHandler.authorizeNamespaceAssignment). Gating the UI on
 * projects:update would render an Add box that always 403s for project owners.
 * Everyone else sees the same list read-only; the server re-checks regardless.
 */
import { useState } from 'react';
import { Layers, Loader2, Plus, X } from 'lucide-react';
import { useAddProjectNamespace, useRemoveProjectNamespace } from './hooks';

/**
 * RFC 1123 label, the rule the apiserver applies to a namespace name. Checked
 * client-side only to give an immediate message; the backend validates too.
 */
const NAMESPACE_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function validateNamespaceName(raw: string): string | null {
  const name = raw.trim();
  if (!name) return 'Enter a namespace name.';
  if (name.length > 63) return 'A namespace name is at most 63 characters.';
  if (!NAMESPACE_PATTERN.test(name)) {
    return 'Use lowercase letters, digits and dashes, starting and ending with a letter or digit.';
  }
  return null;
}

export function ProjectNamespacesCard({
  projectId,
  namespaces,
  canEdit,
}: {
  projectId: string;
  namespaces: string[];
  canEdit: boolean;
}) {
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);
  const addMutation = useAddProjectNamespace(projectId);
  const removeMutation = useRemoveProjectNamespace(projectId);

  const submit = () => {
    const problem = validateNamespaceName(draft);
    if (problem) {
      setError(problem);
      return;
    }
    const name = draft.trim();
    if (namespaces.includes(name)) {
      setError('That namespace is already in this project.');
      return;
    }
    setError(null);
    addMutation.mutate(name, { onSuccess: () => setDraft('') });
  };

  return (
    <section className="rounded-xl border border-border bg-card p-5 space-y-4">
      <header>
        <h2 className="flex items-center gap-2 text-sm font-medium text-foreground">
          <Layers className="h-3.5 w-3.5 text-muted-foreground" />
          Namespaces
        </h2>
        <p className="text-xs text-muted-foreground mt-0.5">
          Members of this project can read and manage workloads in these namespaces, and only
          these. A namespace belongs to one project per cluster.
        </p>
      </header>

      {namespaces.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No namespaces assigned yet — this project&apos;s role bindings currently grant nothing on
          cluster resources.
        </p>
      ) : (
        <ul className="flex flex-wrap gap-2">
          {namespaces.map((ns) => (
            <li
              key={ns}
              className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background pl-2.5 pr-1.5 py-1 text-xs font-mono text-foreground"
            >
              {ns}
              {canEdit && (
                <button
                  type="button"
                  aria-label={`Remove namespace ${ns}`}
                  disabled={removeMutation.isPending}
                  onClick={() => removeMutation.mutate(ns)}
                  className="rounded p-0.5 text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-50"
                >
                  <X className="h-3 w-3" />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {canEdit && (
        <div className="space-y-1.5">
          <div className="flex gap-2">
            <input
              type="text"
              value={draft}
              aria-label="Namespace name"
              placeholder="e.g. team-a"
              onChange={(e) => {
                setDraft(e.target.value);
                if (error) setError(null);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submit();
                }
              }}
              className="h-9 flex-1 min-w-0 rounded-md border border-border bg-background px-3 text-sm font-mono placeholder:text-muted-foreground placeholder:font-sans focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <button
              type="button"
              onClick={submit}
              disabled={addMutation.isPending}
              className="inline-flex items-center gap-1.5 h-9 px-3 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {addMutation.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Plus className="h-3.5 w-3.5" />
              )}
              Add
            </button>
          </div>
          {error && (
            <p role="alert" className="text-xs text-destructive">
              {error}
            </p>
          )}
        </div>
      )}
    </section>
  );
}
