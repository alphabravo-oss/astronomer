import { useState, type ElementType } from 'react';
import { useTabParam } from '@/lib/use-tab-param';
import {
  useGlobalRoles,
  useClusterRoles,
  useProjectRoles,
  useUsers,
  useClusters,
  useClusterRoleBindings,
  useDeleteClusterRoleBinding,
  useDeleteUser,
  useResetUserPassword,
} from '@/lib/hooks';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { RoleEditor } from '@/components/rbac/role-editor';
import { Plus, Shield, Users, Key, Lock, ListChecks } from 'lucide-react';
import type { ClusterRoleBinding, User } from '@/types';
import { ClusterRolesTab, GlobalRolesTab, ProjectRolesTab } from './-roles-tab';
import { UsersTab } from './-users-tab';
import { BindingsTab } from './-bindings-tab';
import { EffectiveTab } from './-effective-tab';
import { CreateUserModal, EditUserModal, ResetPasswordResultModal } from './-user-modal';
import { CreateClusterBindingModal } from './-binding-modal';

export { adminUserHref, isUserLocked, isValidNamespace } from './-utils';

type TabKey = 'global-roles' | 'cluster-roles' | 'project-roles' | 'users' | 'bindings' | 'effective';

const TAB_KEYS = [
  'global-roles',
  'cluster-roles',
  'project-roles',
  'users',
  'bindings',
  'effective',
] as const;

const tabs: { key: TabKey; label: string; icon: ElementType }[] = [
  { key: 'global-roles', label: 'Global Roles', icon: Shield },
  { key: 'cluster-roles', label: 'Cluster Roles', icon: Lock },
  { key: 'project-roles', label: 'Project Roles', icon: Key },
  { key: 'users', label: 'Users', icon: Users },
  { key: 'bindings', label: 'Bindings', icon: Shield },
  { key: 'effective', label: 'Effective', icon: ListChecks },
];

export default function RBACPage() {
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'global-roles');
  const [showRoleEditor, setShowRoleEditor] = useState(false);
  const [showCreateUser, setShowCreateUser] = useState(false);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [resetPasswordResult, setResetPasswordResult] = useState<{ userId: string; password: string } | null>(null);
  const [deleteUserTarget, setDeleteUserTarget] = useState<User | null>(null);
  const [resetPasswordTarget, setResetPasswordTarget] = useState<User | null>(null);
  const [showCreateBinding, setShowCreateBinding] = useState(false);
  const [deleteBindingTarget, setDeleteBindingTarget] = useState<ClusterRoleBinding | null>(null);

  const { data: globalRoles, isLoading: globalLoading, isError: globalError, refetch: refetchGlobal } = useGlobalRoles();
  const { data: clusterRoles, isLoading: clusterLoading, isError: clusterError, refetch: refetchCluster } = useClusterRoles();
  const { data: projectRoles, isLoading: projectLoading, isError: projectError, refetch: refetchProject } = useProjectRoles();
  const { data: usersData, isLoading: usersLoading, isError: usersError, refetch: refetchUsers } = useUsers();
  const { data: clustersData } = useClusters();
  const { data: bindings, isLoading: bindingsLoading, isError: bindingsError, refetch: refetchBindings } = useClusterRoleBindings();

  const deleteUser = useDeleteUser();
  const resetPassword = useResetUserPassword();
  const deleteBinding = useDeleteClusterRoleBinding();

  const users = usersData?.data || [];
  const clusters = clustersData?.data || [];

  const confirmDeleteBinding = async () => {
    if (!deleteBindingTarget) return;
    try {
      await deleteBinding.mutateAsync(deleteBindingTarget.id);
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

  const confirmResetPassword = async () => {
    if (!resetPasswordTarget) return;
    try {
      const result = await resetPassword.mutateAsync(resetPasswordTarget.id);
      setResetPasswordResult({ userId: resetPasswordTarget.id, password: result.temporaryPassword });
    } catch {
      // Error handled by mutation
    }
    setResetPasswordTarget(null);
  };

  const headerActions = (
    <>
      {activeTab === 'users' && (
        <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setShowCreateUser(true)}>
          Create User
        </ActionButton>
      )}
      {(activeTab === 'global-roles' || activeTab === 'cluster-roles' || activeTab === 'project-roles') && (
        <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setShowRoleEditor(true)}>
          Create Role
        </ActionButton>
      )}
      {activeTab === 'bindings' && (
        <ActionButton intent="primary" icon={<Plus className="h-4 w-4" />} onClick={() => setShowCreateBinding(true)}>
          Create Binding
        </ActionButton>
      )}
    </>
  );

  return (
    <PageShell>
      <PageHeader
        title="RBAC"
        description="Users, roles, and bindings. Cluster and project roles can also carry CRD grants for a single Custom Resource."
        actions={headerActions}
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === 'global-roles' && (
          <GlobalRolesTab
            data={globalRoles || []}
            loading={globalLoading}
            isError={globalError}
            onRetry={() => refetchGlobal()}
          />
        )}

        {activeTab === 'cluster-roles' && (
          <ClusterRolesTab
            data={clusterRoles || []}
            loading={clusterLoading}
            isError={clusterError}
            onRetry={() => refetchCluster()}
          />
        )}

        {activeTab === 'project-roles' && (
          <ProjectRolesTab
            data={projectRoles || []}
            loading={projectLoading}
            isError={projectError}
            onRetry={() => refetchProject()}
          />
        )}

        {activeTab === 'users' && (
          <UsersTab
            users={users}
            loading={usersLoading}
            isError={usersError}
            onRetry={() => refetchUsers()}
            onEdit={setEditingUser}
            onResetPassword={setResetPasswordTarget}
            onDelete={setDeleteUserTarget}
          />
        )}

        {activeTab === 'bindings' && (
          <BindingsTab
            bindings={bindings || []}
            clusterRoles={clusterRoles || []}
            clusters={clusters}
            users={users}
            loading={bindingsLoading}
            isError={bindingsError}
            onRetry={() => refetchBindings()}
            onRevoke={setDeleteBindingTarget}
          />
        )}

        {activeTab === 'effective' && <EffectiveTab />}
      </TabsContent>

      {showRoleEditor && <RoleEditor onClose={() => setShowRoleEditor(false)} />}

      {showCreateUser && (
        <CreateUserModal globalRoles={globalRoles || []} onClose={() => setShowCreateUser(false)} />
      )}

      {editingUser && (
        <EditUserModal user={editingUser} globalRoles={globalRoles || []} onClose={() => setEditingUser(null)} />
      )}

      {showCreateBinding && <CreateClusterBindingModal onClose={() => setShowCreateBinding(false)} />}

      {resetPasswordResult && (
        <ResetPasswordResultModal
          password={resetPasswordResult.password}
          onClose={() => setResetPasswordResult(null)}
        />
      )}

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
          deleteBindingTarget?.namespace
            ? `Revoke this cluster role binding scoped to namespace "${deleteBindingTarget.namespace}"? Access granted by it will be removed.`
            : 'Revoke this cluster-wide role binding? Access granted by it will be removed.'
        }
        confirmText="Revoke"
        variant="destructive"
        loading={deleteBinding.isPending}
      />
    </PageShell>
  );
}
