import { lazy, Suspense, useState, type ElementType } from "react";
import { useTabParam } from "@/lib/use-tab-param";
import { useDeleteAccessBinding, useDeleteRole } from "@/lib/hooks/rbac";
import { useDeleteUser, useResetUserPassword } from "@/lib/hooks/user-settings";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabStrip, TabsContent } from "@/components/ui/tabs";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  RoleEditor,
  type EditableRole,
  type RoleEditorMode,
} from "@/components/rbac/role-editor";
import { Plus, Shield, Users, Key, Lock, ListChecks } from "lucide-react";
import type { AccessBinding, User } from "@/types";
import type { RoleScope } from "@/lib/api/rbac";
import { RolesTab } from "./-roles-tab";
import { UsersTab } from "./-users-tab";
import { PagedBindingsTab } from "./-paged-bindings-tab";
import { EffectiveTab } from "./-effective-tab";
import {
  CreateUserModal,
  EditUserModal,
  ResetPasswordResultModal,
} from "./-user-modal";
import { CreateClusterBindingModal } from "@/components/rbac/create-binding-modal";
import {
  bindingTarget,
  roleTitle,
  type RoleLike,
} from "@/components/rbac/binding-utils";

type RoleEditorState = {
  mode: RoleEditorMode;
  scope: RoleScope;
  role?: EditableRole;
};

const NativeRulesTab = lazy(() => import("./-native-rules-tab"));

type ConcreteRole = RoleLike & { id: string };
type DeleteRoleTarget = { scope: RoleScope; role: ConcreteRole };

function editableRole(scope: RoleScope, role: ConcreteRole): EditableRole {
  return {
    id: role.id,
    name: role.name,
    displayName: roleTitle(role),
    description: role.description ?? "",
    scope,
    rules: (role.rules ?? []).map((rule) => ({
      resource: rule.resource ?? rule.resources?.[0] ?? "",
      verbs: rule.verbs,
      api_groups: rule.apiGroups ?? rule.api_groups,
    })),
  };
}

export {
  adminUserHref,
  isUserLocked,
  isValidNamespace,
} from "@/components/rbac/binding-utils";

type TabKey =
  | "global-roles"
  | "cluster-roles"
  | "project-roles"
  | "users"
  | "bindings"
  | "native-rules"
  | "effective";

const TAB_KEYS = [
  "global-roles",
  "cluster-roles",
  "project-roles",
  "users",
  "bindings",
  "native-rules",
  "effective",
] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: "global-roles", label: "Global Roles", icon: Shield },
  { key: "cluster-roles", label: "Cluster Roles", icon: Lock },
  { key: "project-roles", label: "Project Roles", icon: Key },
  { key: "users", label: "Users", icon: Users },
  { key: "bindings", label: "Bindings", icon: Shield },
  { key: "native-rules", label: "Native grants", icon: Key },
  { key: "effective", label: "Effective", icon: ListChecks },
];

export default function RBACPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, "global-roles");
  const [roleEditorState, setRoleEditorState] =
    useState<RoleEditorState | null>(null);
  const [deleteRoleTarget, setDeleteRoleTarget] =
    useState<DeleteRoleTarget | null>(null);
  const [showCreateUser, setShowCreateUser] = useState(false);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [resetPasswordResult, setResetPasswordResult] = useState<{
    userId: string;
    password: string;
  } | null>(null);
  const [deleteUserTarget, setDeleteUserTarget] = useState<User | null>(null);
  const [resetPasswordTarget, setResetPasswordTarget] = useState<User | null>(
    null,
  );
  const [showCreateBinding, setShowCreateBinding] = useState(false);
  const [deleteBindingTarget, setDeleteBindingTarget] =
    useState<AccessBinding | null>(null);

  const deleteUser = useDeleteUser();
  const resetPassword = useResetUserPassword();
  const deleteBinding = useDeleteAccessBinding();
  const deleteRole = useDeleteRole();

  const confirmDeleteBinding = async () => {
    if (!deleteBindingTarget) return;
    try {
      await deleteBinding.mutateAsync(deleteBindingTarget);
    } catch {
      // Error handled by mutation
    }
    setDeleteBindingTarget(null);
  };

  const confirmDeleteUser = async () => {
    if (!deleteUserTarget) return;
    try {
      await deleteUser.mutateAsync(deleteUserTarget.id);
    } catch {
      // Error handled by mutation
    }
    setDeleteUserTarget(null);
  };

  const confirmDeleteRole = async () => {
    if (!deleteRoleTarget) return;
    try {
      await deleteRole.mutateAsync({
        scope: deleteRoleTarget.scope,
        id: deleteRoleTarget.role.id,
      });
      setDeleteRoleTarget(null);
    } catch {
      // Error handled by mutation; leave the dialog open for retry.
    }
  };

  const roleActions = (scope: RoleScope) => ({
    onEdit: (role: ConcreteRole) =>
      setRoleEditorState({
        mode: "edit",
        scope,
        role: editableRole(scope, role),
      }),
    onDuplicate: (role: ConcreteRole) =>
      setRoleEditorState({
        mode: "duplicate",
        scope,
        role: editableRole(scope, role),
      }),
    onDelete: (role: ConcreteRole) => setDeleteRoleTarget({ scope, role }),
  });

  const confirmResetPassword = async () => {
    if (!resetPasswordTarget) return;
    try {
      const result = await resetPassword.mutateAsync(resetPasswordTarget.id);
      setResetPasswordResult({
        userId: resetPasswordTarget.id,
        password: result.temporaryPassword,
      });
    } catch {
      // Error handled by mutation
    }
    setResetPasswordTarget(null);
  };

  const createRoleScope =
    activeTab === "global-roles"
      ? "global"
      : activeTab === "project-roles"
        ? "project"
        : "cluster";

  const headerActions = (
    <>
      {activeTab === "users" && (
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => setShowCreateUser(true)}
        >
          Create User
        </ActionButton>
      )}
      {(activeTab === "global-roles" ||
        activeTab === "cluster-roles" ||
        activeTab === "project-roles") && (
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() =>
            setRoleEditorState({ mode: "create", scope: createRoleScope })
          }
        >
          Create Role
        </ActionButton>
      )}
      {activeTab === "bindings" && (
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => setShowCreateBinding(true)}
        >
          Create Binding
        </ActionButton>
      )}
    </>
  );

  return (
    <PageShell>
      <PageHeader
        title="RBAC"
        description="Users, roles, and bindings. Cluster and project roles can also carry CRD grants for a single Custom Resource. Effective shows what a user can actually do."
        actions={headerActions}
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === "global-roles" && (
          <RolesTab key="global" scope="global" {...roleActions("global")} />
        )}

        {activeTab === "cluster-roles" && (
          <RolesTab key="cluster" scope="cluster" {...roleActions("cluster")} />
        )}

        {activeTab === "project-roles" && (
          <RolesTab key="project" scope="project" {...roleActions("project")} />
        )}

        {activeTab === "users" && (
          <UsersTab
            onEdit={setEditingUser}
            onResetPassword={setResetPasswordTarget}
            onDelete={setDeleteUserTarget}
          />
        )}

        {activeTab === "bindings" && (
          <PagedBindingsTab onRevoke={setDeleteBindingTarget} />
        )}

        {activeTab === "effective" && <EffectiveTab />}
        {activeTab === "native-rules" && (
          <Suspense fallback={<p role="status">Loading native grants…</p>}>
            <NativeRulesTab />
          </Suspense>
        )}
      </TabsContent>

      {roleEditorState && (
        <RoleEditor
          key={`${roleEditorState.mode}-${roleEditorState.scope}-${roleEditorState.role?.id ?? "new"}`}
          onClose={() => setRoleEditorState(null)}
          mode={roleEditorState.mode}
          defaultScope={roleEditorState.scope}
          initialRole={roleEditorState.role}
        />
      )}

      {showCreateUser && (
        <CreateUserModal onClose={() => setShowCreateUser(false)} />
      )}

      {editingUser && (
        <EditUserModal
          user={editingUser}
          onClose={() => setEditingUser(null)}
        />
      )}

      {showCreateBinding && (
        <CreateClusterBindingModal
          onClose={() => setShowCreateBinding(false)}
        />
      )}

      {resetPasswordResult && (
        <ResetPasswordResultModal
          password={resetPasswordResult.password}
          onClose={() => setResetPasswordResult(null)}
        />
      )}

      <ConfirmDialog
        open={!!deleteRoleTarget}
        onClose={() => setDeleteRoleTarget(null)}
        onConfirm={confirmDeleteRole}
        title="Delete Role"
        description={`Delete custom role "${deleteRoleTarget ? roleTitle(deleteRoleTarget.role) : ""}"? Existing bindings to it will also be removed.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleteRole.isPending}
      />

      <ConfirmDialog
        open={!!deleteUserTarget}
        onClose={() => setDeleteUserTarget(null)}
        onConfirm={confirmDeleteUser}
        title="Delete User"
        description={`Delete user "${deleteUserTarget?.displayName || deleteUserTarget?.username}"? This action cannot be undone.`}
        confirmText="Delete"
        variant="destructive"
        loading={deleteUser.isPending}
      />

      <ConfirmDialog
        open={!!resetPasswordTarget}
        onClose={() => setResetPasswordTarget(null)}
        onConfirm={confirmResetPassword}
        title="Reset Password"
        description={`Reset password for "${resetPasswordTarget?.displayName || resetPasswordTarget?.username}"? A new temporary password will be generated.`}
        confirmText="Reset Password"
        loading={resetPassword.isPending}
      />

      <ConfirmDialog
        open={!!deleteBindingTarget}
        onClose={() => setDeleteBindingTarget(null)}
        onConfirm={confirmDeleteBinding}
        title="Revoke Binding"
        description={
          deleteBindingTarget
            ? `Revoke this ${deleteBindingTarget.scope} binding for ${bindingTarget(deleteBindingTarget, [], [])}? Access granted by it will be removed.`
            : "Revoke this role binding? Access granted by it will be removed."
        }
        confirmText="Revoke"
        variant="destructive"
        loading={deleteBinding.isPending}
      />
    </PageShell>
  );
}
