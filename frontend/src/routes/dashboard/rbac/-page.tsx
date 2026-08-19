import { useMemo, useState, type ElementType } from 'react';
import { useTabParam } from '@/lib/use-tab-param';
import {
  useGlobalRoles,
  useClusterRoles,
  useProjectRoles,
  useUsers,
  useClusters,
  useProjects,
  useClusterRoleBindings,
  useGlobalRoleBindings,
  useProjectRoleBindings,
  useDeleteUser,
  useResetUserPassword,
  useDeleteAccessBinding,
} from '@/lib/hooks';
import { PageHeader, PageShell } from '@/components/ui/page';
import { TabStrip, TabsContent } from '@/components/ui/tabs';
import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { RoleEditor } from '@/components/rbac/role-editor';
import { Plus, Shield, Users, Key, Lock, ListChecks } from 'lucide-react';
import type { AccessBinding, User } from '@/types';
import { ClusterRolesTab, GlobalRolesTab, ProjectRolesTab } from './-roles-tab';
import { UsersTab } from './-users-tab';
import { BindingsTab } from './-bindings-tab';
import { EffectiveTab } from './-effective-tab';
import { CreateUserModal, EditUserModal, ResetPasswordResultModal } from './-user-modal';
import { CreateClusterBindingModal } from './-binding-modal';
import { bindingTarget, roleTitle, toAccessBinding } from './-utils';

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
  const [deleteBindingTarget, setDeleteBindingTarget] = useState<AccessBinding | null>(null);

  const { data: globalRoles, isLoading: globalLoading, isError: globalError, refetch: refetchGlobal } = useGlobalRoles();
  const { data: clusterRoles, isLoading: clusterLoading, isError: clusterError, refetch: refetchCluster } = useClusterRoles();
  const { data: projectRoles, isLoading: projectLoading, isError: projectError, refetch: refetchProject } = useProjectRoles();
  const { data: usersData, isLoading: usersLoading, isError: usersError, refetch: refetchUsers } = useUsers({
    pageSize: 200,
  });
  const { data: clustersData } = useClusters({ pageSize: 200 });
  const { data: projectsData } = useProjects({ pageSize: 200 });
  const {
    data: clusterBindings,
    isLoading: clusterBindingsLoading,
    isError: clusterBindingsError,
    refetch: refetchClusterBindings,
  } = useClusterRoleBindings();
  const {
    data: globalBindings,
    isLoading: globalBindingsLoading,
    isError: globalBindingsError,
    refetch: refetchGlobalBindings,
  } = useGlobalRoleBindings();
  const {
    data: projectBindings,
    isLoading: projectBindingsLoading,
    isError: projectBindingsError,
    refetch: refetchProjectBindings,
  } = useProjectRoleBindings();

  const deleteUser = useDeleteUser();
  const resetPassword = useResetUserPassword();
  const deleteBinding = useDeleteAccessBinding();

  const clusters = clustersData?.data || [];
  const projects = projectsData?.data || [];
  const globalRoleList = globalRoles || [];
  const clusterRoleList = clusterRoles || [];
  const projectRoleList = projectRoles || [];

  const bindings = useMemo(() => {
    const global = (globalBindings || []).map((row) => toAccessBinding('global', row));
    const cluster = (clusterBindings || []).map((row) => toAccessBinding('cluster', row));
    const project = (projectBindings || []).map((row) => toAccessBinding('project', row));
    return [...global, ...cluster, ...project];
  }, [globalBindings, clusterBindings, projectBindings]);

  const users = useMemo(() => {
    const roleNameById = new Map(globalRoleList.map((role) => [role.id, roleTitle(role)]));
    const rolesByUser = new Map<string, string[]>();
    for (const binding of bindings) {
      if (binding.scope !== 'global' || !binding.userId) continue;
      const name = roleNameById.get(binding.roleId) || binding.roleId;
      const current = rolesByUser.get(binding.userId) ?? [];
      if (!current.includes(name)) current.push(name);
      rolesByUser.set(binding.userId, current);
    }
    return (usersData?.data || []).map((user) => ({
      ...user,
      globalRoles: user.globalRoles?.length ? user.globalRoles : rolesByUser.get(user.id) ?? [],
    }));
  }, [usersData, bindings, globalRoleList]);

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

  const createRoleScope =
    activeTab === 'global-roles' ? 'global' : activeTab === 'project-roles' ? 'project' : 'cluster';

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
        description="Users, roles, and bindings. Cluster and project roles can also carry CRD grants for a single Custom Resource. Effective shows what a user can actually do."
        actions={headerActions}
      />

      <TabStrip tabs={tabs} value={activeTab} onChange={setActiveTab} />

      <TabsContent>
        {activeTab === 'global-roles' && (
          <GlobalRolesTab
            data={globalRoleList}
            loading={globalLoading}
            isError={globalError}
            onRetry={() => refetchGlobal()}
          />
        )}

        {activeTab === 'cluster-roles' && (
          <ClusterRolesTab
            data={clusterRoleList}
            loading={clusterLoading}
            isError={clusterError}
            onRetry={() => refetchCluster()}
          />
        )}

        {activeTab === 'project-roles' && (
          <ProjectRolesTab
            data={projectRoleList}
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
            bindings={bindings}
            globalRoles={globalRoleList}
            clusterRoles={clusterRoleList}
            projectRoles={projectRoleList}
            clusters={clusters}
            projects={projects}
            users={users}
            loading={globalBindingsLoading || clusterBindingsLoading || projectBindingsLoading}
            isError={globalBindingsError || clusterBindingsError || projectBindingsError}
            onRetry={() => {
              void refetchGlobalBindings();
              void refetchClusterBindings();
              void refetchProjectBindings();
            }}
            onRevoke={setDeleteBindingTarget}
          />
        )}

        {activeTab === 'effective' && <EffectiveTab />}
      </TabsContent>

      {showRoleEditor && (
        <RoleEditor onClose={() => setShowRoleEditor(false)} defaultScope={createRoleScope} />
      )}

      {showCreateUser && (
        <CreateUserModal globalRoles={globalRoleList} onClose={() => setShowCreateUser(false)} />
      )}

      {editingUser && (
        <EditUserModal user={editingUser} globalRoles={globalRoleList} onClose={() => setEditingUser(null)} />
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
          deleteBindingTarget
            ? `Revoke this ${deleteBindingTarget.scope} binding for ${bindingTarget(deleteBindingTarget, clusters, projects)}? Access granted by it will be removed.`
            : 'Revoke this role binding? Access granted by it will be removed.'
        }
        confirmText="Revoke"
        variant="destructive"
        loading={deleteBinding.isPending}
      />
    </PageShell>
  );
}
