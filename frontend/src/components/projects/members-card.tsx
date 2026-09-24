/**
 * Project · members.
 *
 * `Project.members` (the wire field) is a dead stub — the project GET never
 * populates it (see `lib/api/project-detail.ts`'s `mapProject`). The actual
 * roster is the project-scoped RBAC binding list: one binding per
 * principal/role pair on this project, exactly what `-bindings-tab.tsx`
 * renders on the RBAC admin page filtered to `scope === "project"`. This
 * card is that same list, scoped to one project, with Add/Remove.
 */
import { useState } from "react";
import { Plus, Trash2, Users } from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { QueryStates } from "@/components/ui/query-states";
import { useDeleteAccessBinding } from "@/lib/hooks/rbac";
import { useProjectBindingPage } from "@/lib/hooks/project-binding-page";
import { useBindingNames } from "@/components/rbac/use-binding-names";
import { pageCountLabel } from "@/lib/api/pagination";
import {
  permissionDeniedReason,
  usePermissionDecision,
} from "@/lib/permission-hooks";
import { bindingSubject, roleTitle } from "@/components/rbac/binding-utils";
import { CreateClusterBindingModal } from "@/components/rbac/create-binding-modal";
import type { AccessBinding, ProjectRole } from "@/types";

/** DOM id the overview page's Members MetricCard links to (`href="#..."`). */
export const PROJECT_MEMBERS_CARD_ID = "project-members";

function roleName(binding: AccessBinding, roles: ProjectRole[]): string {
  const role = roles.find((r) => r.id === binding.roleId);
  return role ? roleTitle(role) : binding.roleId;
}

export function ProjectMembersCard({ projectId }: { projectId: string }) {
  return <ProjectMembersPage key={projectId} projectId={projectId} />;
}

function ProjectMembersPage({ projectId }: { projectId: string }) {
  const [pageIndex, setPageIndex] = useState(0);
  const bindingsQuery = useProjectBindingPage(projectId, pageIndex);
  const deleteBinding = useDeleteAccessBinding();
  const [removeTarget, setRemoveTarget] = useState<AccessBinding | null>(null);
  const [showAdd, setShowAdd] = useState(false);

  const scope = { type: "project" as const, id: projectId };
  const addPermission = usePermissionDecision("rbac", "create", scope);
  const removePermission = usePermissionDecision("rbac", "delete", scope);

  const bindings = bindingsQuery.isError
    ? []
    : (bindingsQuery.data?.data ?? []);
  const { users, roles } = useBindingNames(bindings, "project");

  return (
    <section
      id={PROJECT_MEMBERS_CARD_ID}
      className="rounded-xl border border-border bg-card p-5 space-y-4"
    >
      <header>
        <h2 className="flex items-center gap-2 text-sm font-medium text-foreground">
          <Users className="h-3.5 w-3.5 text-muted-foreground" />
          Members
        </h2>
        <p className="text-xs text-muted-foreground mt-0.5">
          Project-scoped role assignments. A member can have more than one
          binding.
        </p>
      </header>

      <QueryStates
        query={bindingsQuery}
        loadingTitle="Loading members…"
        errorTitle="Could not load members"
        permissionDescription="You do not have permission to view this project's members."
        isEmpty={(data) => data.data.length === 0}
        empty={
          <p className="text-xs text-muted-foreground">
            {pageIndex === 0 ? "No members yet." : "No members on this page."}
          </p>
        }
      >
        <ul className="divide-y divide-border/60">
          {bindings.map((binding) => {
            const subject = bindingSubject(binding, users);
            return (
              <li
                key={binding.id}
                className="flex items-center justify-between gap-3 py-2 text-sm"
              >
                <div className="min-w-0">
                  <p className="truncate font-medium text-foreground">
                    {subject}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {binding.userId ? "User" : "Group"} ·{" "}
                    {roleName(binding, roles)}
                  </p>
                </div>
                <ActionButton
                  size="icon"
                  intent="ghost"
                  aria-label={`Remove ${subject}`}
                  icon={<Trash2 className="h-3.5 w-3.5" />}
                  onClick={() => setRemoveTarget(binding)}
                  disabled={!removePermission.allowed}
                  disabledReason={permissionDeniedReason(removePermission)}
                />
              </li>
            );
          })}
        </ul>
      </QueryStates>

      {!bindingsQuery.isError && bindingsQuery.data && (
        <p className="text-xs text-muted-foreground">
          {pageCountLabel(bindingsQuery.data)} role bindings · Page{" "}
          {pageIndex + 1}
        </p>
      )}
      {(pageIndex > 0 ||
        (!bindingsQuery.isError &&
          bindingsQuery.data?.pagination.has_more)) && (
        <nav aria-label="Member pages" className="flex gap-2">
          <ActionButton
            size="sm"
            disabled={pageIndex === 0}
            onClick={() => setPageIndex((page) => page - 1)}
          >
            Previous page
          </ActionButton>
          <ActionButton
            size="sm"
            disabled={
              bindingsQuery.isFetching ||
              bindingsQuery.isError ||
              !bindingsQuery.data?.pagination.has_more
            }
            onClick={() => setPageIndex((page) => page + 1)}
          >
            Next page
          </ActionButton>
        </nav>
      )}

      <ActionButton
        size="sm"
        icon={<Plus className="h-3.5 w-3.5" />}
        onClick={() => setShowAdd(true)}
        disabled={!addPermission.allowed}
        disabledReason={permissionDeniedReason(addPermission)}
      >
        Add member
      </ActionButton>

      {showAdd && (
        <CreateClusterBindingModal
          onClose={() => setShowAdd(false)}
          fixedScope={{ kind: "project", projectId }}
        />
      )}

      <ConfirmDialog
        open={!!removeTarget}
        onClose={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (!removeTarget) return;
          deleteBinding.mutate(
            { scope: "project", id: removeTarget.id },
            { onSuccess: () => setRemoveTarget(null) },
          );
        }}
        title="Remove member"
        description={`This revokes ${
          removeTarget ? bindingSubject(removeTarget, users) : "this member"
        }'s project role binding.`}
        confirmText="Remove"
        variant="destructive"
        loading={deleteBinding.isPending}
      />
    </section>
  );
}
