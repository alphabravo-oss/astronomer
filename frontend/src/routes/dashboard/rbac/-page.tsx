import { useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { useTabParam } from '@/lib/use-tab-param';
import {
  useGlobalRoles,
  useClusterRoles,
  useProjectRoles,
  useUsers,
  useClusters,
  useClusterRoleBindings,
  useCreateClusterRoleBinding,
  useDeleteClusterRoleBinding,
  useCreateUser,
  useUpdateUser,
  useDeleteUser,
  useResetUserPassword,
  useMyEffectivePermissions,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { RoleEditor } from '@/components/rbac/role-editor';
import { StatusBadge } from '@/components/ui/status-badge';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { formatRelativeTime, cn } from '@/lib/utils';
import type {
  ClusterRole,
  ClusterRoleBinding,
  EffectivePermissionBinding,
  EffectivePermissionGrant,
  EffectivePermissionSource,
  GlobalRole,
  ProjectRole,
  User,
} from '@/types';
import {
  Shield,
  Plus,
  Users,
  Key,
  Lock,
  Pencil,
  Trash2,
  RotateCcw,
  X,
  Loader2,
  Eye,
  EyeOff,
  Copy,
  ListChecks,
} from 'lucide-react';
import { toastError, toastSuccess } from '@/lib/toast';
import { copyToClipboard } from '@/lib/utils';

type TabKey = 'global-roles' | 'cluster-roles' | 'project-roles' | 'users' | 'bindings' | 'effective';

const TAB_KEYS = [
  'global-roles',
  'cluster-roles',
  'project-roles',
  'users',
  'bindings',
  'effective',
] as const;

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'global-roles', label: 'Global Roles', icon: Shield },
  { key: 'cluster-roles', label: 'Cluster Roles', icon: Lock },
  { key: 'project-roles', label: 'Project Roles', icon: Key },
  { key: 'users', label: 'Users', icon: Users },
  { key: 'bindings', label: 'Bindings', icon: Shield },
  { key: 'effective', label: 'Effective', icon: ListChecks },
];

/** In-app route for the admin user-security detail (unlock, force-logout, ...). */
export function adminUserHref(userId: string): string {
  return `/dashboard/admin/users/${userId}`;
}

/** True while the account is locked out (locked_until is a future timestamp). */
export function isUserLocked(user: Pick<User, 'lockedUntil' | 'locked_until'>): boolean {
  const raw = user.lockedUntil ?? user.locked_until;
  if (!raw) return false;
  const until = Date.parse(raw);
  return Number.isFinite(until) && until > Date.now();
}

/**
 * DNS-1123 label check mirroring the backend's k8svalidation.IsDNS1123Label on
 * POST /rbac/cluster-role-bindings/. An empty value is valid client-side (it
 * means "cluster-wide"); a non-empty value must be a lowercase alphanumeric
 * label (dashes allowed internally), at most 63 characters.
 */
export function isValidNamespace(namespace: string): boolean {
  if (namespace === '') return true;
  return namespace.length <= 63 && /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/.test(namespace);
}

export default function RBACPage() {
  const router = useRouter();
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

  const clusterRoleNameById = new Map((clusterRoles || []).map((r) => [r.id, r.displayName || r.name]));
  const clusterNameById = new Map(clusters.map((c) => [c.id, c.name]));
  const userLabelById = new Map(users.map((u) => [u.id, u.displayName || u.username]));

  const confirmDeleteBinding = async () => {
    if (!deleteBindingTarget) return;
    try {
      await deleteBinding.mutateAsync(deleteBindingTarget.id);
    } catch {
      // Error handled by mutation
    }
    setDeleteBindingTarget(null);
  };

  const handleDeleteUser = (user: User) => setDeleteUserTarget(user);
  const handleResetPassword = (user: User) => setResetPasswordTarget(user);

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

  const globalRoleColumns: Column<GlobalRole>[] = [
    {
      key: 'name',
      header: 'Role',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.displayName}</p>
            <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.description || '--'}</span>,
      sortable: false,
    },
    {
      key: 'builtin',
      header: 'Type',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded', row.builtin ? 'bg-muted text-muted-foreground' : 'bg-status-info/10 text-status-info')}>
          {row.builtin ? 'Built-in' : 'Custom'}
        </span>
      ),
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums text-sm">{row.rules.length}</span>,
      sortAccessor: (row) => row.rules.length,
      align: 'center',
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
  ];

  const clusterRoleColumns: Column<ClusterRole>[] = [
    {
      key: 'name',
      header: 'Role',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.displayName}</p>
          <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
        </div>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.clusterName}</span>,
    },
    {
      key: 'builtin',
      header: 'Type',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded', row.builtin ? 'bg-muted text-muted-foreground' : 'bg-status-info/10 text-status-info')}>
          {row.builtin ? 'Built-in' : 'Custom'}
        </span>
      ),
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums text-sm">{row.rules.length}</span>,
      align: 'center',
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
  ];

  const projectRoleColumns: Column<ProjectRole>[] = [
    {
      key: 'name',
      header: 'Role',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.displayName}</p>
          <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
        </div>
      ),
    },
    {
      key: 'project',
      header: 'Project',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.projectName}</span>,
    },
    {
      key: 'builtin',
      header: 'Type',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded', row.builtin ? 'bg-muted text-muted-foreground' : 'bg-status-info/10 text-status-info')}>
          {row.builtin ? 'Built-in' : 'Custom'}
        </span>
      ),
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums text-sm">{row.rules.length}</span>,
      align: 'center',
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
    },
  ];

  const userColumns: Column<User>[] = [
    {
      key: 'name',
      header: 'User',
      accessor: (row) => (
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-full bg-gradient-to-br from-zinc-600 to-zinc-800 flex items-center justify-center flex-shrink-0">
            <span className="text-xs font-medium text-zinc-300">
              {(row.displayName || row.username).charAt(0).toUpperCase()}
            </span>
          </div>
          <div>
            <p className="font-medium text-foreground">{row.displayName}</p>
            <p className="text-xs text-muted-foreground">{row.username}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'email',
      header: 'Email',
      accessor: (row) => <span className="text-sm text-muted-foreground">{row.email}</span>,
    },
    {
      key: 'provider',
      header: 'Provider',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.provider}
        </span>
      ),
    },
    {
      key: 'roles',
      header: 'Global Roles',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.globalRoles.map((role) => (
            <span key={role} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              {role}
            </span>
          ))}
        </div>
      ),
    },
    {
      key: 'enabled',
      header: 'Status',
      accessor: (row) => (
        <div className="flex items-center gap-1.5">
          <StatusBadge status={row.enabled ? 'active' : 'disconnected'} label={row.enabled ? 'Enabled' : 'Disabled'} />
          {isUserLocked(row) && (
            <span
              className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-status-error/10 text-status-error"
              title="Account is locked out — open the user to unlock"
            >
              <Lock className="h-3 w-3" />
              Locked
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'lastLogin',
      header: 'Last Login',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.lastLogin)}</span>,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => setEditingUser(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Edit user"
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => handleResetPassword(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Reset password"
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => handleDeleteUser(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete user"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const bindingColumns: Column<ClusterRoleBinding>[] = [
    {
      key: 'subject',
      header: 'Subject',
      accessor: (row) => (
        <span className="font-medium text-foreground">
          {row.user_id ? userLabelById.get(row.user_id) || row.user_id : `group: ${row.group}`}
        </span>
      ),
    },
    {
      key: 'role',
      header: 'Cluster Role',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{clusterRoleNameById.get(row.role_id) || row.role_id}</span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{clusterNameById.get(row.cluster_id) || row.cluster_id}</span>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) =>
        row.namespace ? (
          <span className="font-mono text-xs text-foreground">{row.namespace}</span>
        ) : (
          <span className="text-xs text-muted-foreground">cluster-wide</span>
        ),
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.created_at)}</span>,
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => setDeleteBindingTarget(row)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Revoke binding"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">RBAC</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Role-Based Access Control management
          </p>
        </div>
        <div className="flex items-center gap-2">
          {activeTab === 'users' && (
            <button
              onClick={() => setShowCreateUser(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create User
            </button>
          )}
          {(activeTab === 'global-roles' || activeTab === 'cluster-roles' || activeTab === 'project-roles') && (
            <button
              onClick={() => setShowRoleEditor(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Role
            </button>
          )}
          {activeTab === 'bindings' && (
            <button
              onClick={() => setShowCreateBinding(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Binding
            </button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-6">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            return (
              <button
                key={tab.key}
                onClick={() => setActiveTab(tab.key)}
                className={cn(
                  'flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors',
                  activeTab === tab.key
                    ? 'border-foreground text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground'
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </button>
            );
          })}
        </nav>
      </div>

      {/* Content */}
      <div className="animate-fade-in">
        {activeTab === 'global-roles' && (
          <DataTable
            data={globalRoles || []}
            columns={globalRoleColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search global roles..."
            loading={globalLoading}
            isError={globalError}
            onRetry={() => refetchGlobal()}
            emptyMessage="No global roles defined"
          />
        )}

        {activeTab === 'cluster-roles' && (
          <DataTable
            data={clusterRoles || []}
            columns={clusterRoleColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search cluster roles..."
            loading={clusterLoading}
            isError={clusterError}
            onRetry={() => refetchCluster()}
            emptyMessage="No cluster roles defined"
          />
        )}

        {activeTab === 'project-roles' && (
          <DataTable
            data={projectRoles || []}
            columns={projectRoleColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search project roles..."
            loading={projectLoading}
            isError={projectError}
            onRetry={() => refetchProject()}
            emptyMessage="No project roles defined"
          />
        )}

        {activeTab === 'users' && (
          <DataTable
            data={users}
            columns={userColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search users..."
            loading={usersLoading}
            isError={usersError}
            onRetry={() => refetchUsers()}
            onRowClick={(row) => router.push(adminUserHref(row.id))}
            emptyMessage="No users found"
          />
        )}

        {activeTab === 'bindings' && (
          <DataTable
            data={bindings || []}
            columns={bindingColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search bindings..."
            loading={bindingsLoading}
            isError={bindingsError}
            onRetry={() => refetchBindings()}
            emptyMessage="No role bindings found"
          />
        )}

        {activeTab === 'effective' && <EffectivePermissionsPanel />}
      </div>

      {/* Role Editor Modal */}
      {showRoleEditor && (
        <RoleEditor onClose={() => setShowRoleEditor(false)} />
      )}

      {/* Create User Modal */}
      {showCreateUser && (
        <CreateUserModal
          globalRoles={globalRoles || []}
          onClose={() => setShowCreateUser(false)}
        />
      )}

      {/* Edit User Modal */}
      {editingUser && (
        <EditUserModal
          user={editingUser}
          globalRoles={globalRoles || []}
          onClose={() => setEditingUser(null)}
        />
      )}

      {/* Create Cluster Binding Modal */}
      {showCreateBinding && (
        <CreateClusterBindingModal onClose={() => setShowCreateBinding(false)} />
      )}

      {/* Reset Password Result */}
      {resetPasswordResult && (
        <ResetPasswordResultModal
          password={resetPasswordResult.password}
          onClose={() => setResetPasswordResult(null)}
        />
      )}

      {/* Delete User Confirmation */}
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

      {/* Reset Password Confirmation */}
      <ConfirmDialog
        open={!!resetPasswordTarget}
        onClose={() => setResetPasswordTarget(null)}
        onConfirm={confirmResetPassword}
        title="Reset Password"
        description={`Reset password for "${resetPasswordTarget?.displayName || resetPasswordTarget?.username}"? A new temporary password will be generated.`}
        confirmText="Reset Password"
        loading={resetPassword.isPending}
      />

      {/* Revoke Binding Confirmation */}
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
    </div>
  );
}

// ============================================================
// Create Cluster Binding Modal
// ============================================================

function CreateClusterBindingModal({ onClose }: { onClose: () => void }) {
  const { data: usersData } = useUsers();
  const { data: clusterRoles } = useClusterRoles();
  const { data: clustersData } = useClusters();
  const createBinding = useCreateClusterRoleBinding();

  const users = usersData?.data || [];
  const clusters = clustersData?.data || [];
  const roles = clusterRoles || [];

  const [form, setForm] = useState({ userId: '', roleId: '', clusterId: '', namespace: '' });

  const namespaceValid = isValidNamespace(form.namespace.trim());
  const canSubmit = !!form.userId && !!form.roleId && !!form.clusterId && namespaceValid;

  const handleSave = async () => {
    if (!canSubmit) {
      toastError('Select a user, cluster role, and cluster; namespace must be a valid label');
      return;
    }
    try {
      await createBinding.mutateAsync({
        user_id: form.userId,
        role_id: form.roleId,
        cluster_id: form.clusterId,
        namespace: form.namespace.trim() || undefined,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create Cluster Binding</h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">User</label>
            <select
              value={form.userId}
              onChange={(e) => setForm((f) => ({ ...f, userId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a user…</option>
              {users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.displayName || u.username}
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster Role</label>
            <select
              value={form.roleId}
              onChange={(e) => setForm((f) => ({ ...f, roleId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a cluster role…</option>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.displayName || r.name}
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster</label>
            <select
              value={form.clusterId}
              onChange={(e) => setForm((f) => ({ ...f, clusterId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a cluster…</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Namespace</label>
            <input
              type="text"
              value={form.namespace}
              onChange={(e) => setForm((f) => ({ ...f, namespace: e.target.value }))}
              placeholder="leave blank for cluster-wide"
              className={cn(
                'w-full h-9 px-3 rounded-md border bg-background text-sm font-mono placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring',
                namespaceValid ? 'border-border' : 'border-status-error'
              )}
            />
            {!namespaceValid && (
              <p className="text-xs text-status-error">
                Must be a valid Kubernetes namespace (lowercase alphanumeric and dashes, ≤63 chars).
              </p>
            )}
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createBinding.isPending || !canSubmit}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createBinding.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create Binding
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

function EffectivePermissionsPanel() {
  const [context, setContext] = useState({ clusterId: '', projectId: '', namespace: '' });
  const selectedContext = {
    clusterId: context.clusterId.trim() || undefined,
    projectId: context.projectId.trim() || undefined,
    namespace: context.namespace.trim() || undefined,
  };
  const { data, isLoading, isError, refetch } = useMyEffectivePermissions(selectedContext);
  const permissions = data?.permissions || [];
  const bindings = data?.bindings || [];
  const responseContext = data?.context;
  const resourceCount = new Set(permissions.map((p) => p.resource)).size;
  const highRiskCount = permissions.filter(isHighRiskGrant).length;
  const applicableCount = permissions.filter((p) => p.appliesToContext !== false).length;

  const permissionColumns: Column<EffectivePermissionGrant>[] = [
    {
      key: 'applies',
      header: 'Applies',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded', row.appliesToContext === false ? 'bg-muted text-muted-foreground' : 'bg-status-success/10 text-status-success')}>
          {row.appliesToContext === false ? 'No' : 'Yes'}
        </span>
      ),
      sortAccessor: (row) => (row.appliesToContext === false ? 0 : 1),
    },
    {
      key: 'resource',
      header: 'Resource',
      accessor: (row) => row.resource,
      sortAccessor: (row) => row.resource,
    },
    {
      key: 'verb',
      header: 'Verb',
      accessor: (row) => row.verb,
      sortAccessor: (row) => row.verb,
    },
    {
      key: 'risk',
      header: 'Risk',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded', riskClassName(row))}>
          {riskLabel(row)}
        </span>
      ),
      sortAccessor: (row) => riskSort(row),
    },
    {
      key: 'sources',
      header: 'Granted By',
      accessor: (row) => sourceSummary(row.sources),
      sortable: false,
    },
    {
      key: 'target',
      header: 'Scope Target',
      accessor: (row) => targetSummary(row.sources),
      sortable: false,
    },
  ];

  const bindingColumns: Column<EffectivePermissionBinding>[] = [
    {
      key: 'role',
      header: 'Role',
      accessor: (row) => row.roleName || row.roleId || row.bindingId || row.scope,
    },
    {
      key: 'scope',
      header: 'Scope',
      accessor: (row) => row.scope || 'global',
    },
    {
      key: 'target',
      header: 'Target',
      accessor: (row) => bindingTarget(row),
      sortable: false,
    },
    {
      key: 'rules',
      header: 'Rules',
      accessor: (row) => <span className="tabular-nums">{row.rules.length}</span>,
      sortAccessor: (row) => row.rules.length,
      align: 'center',
    },
  ];

  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-4">
        <MetricTile label="Grants" value={permissions.length} />
        <MetricTile label="Bindings" value={bindings.length} />
        <MetricTile label="Resources" value={resourceCount} />
        <MetricTile label="Applies Here" value={applicableCount} />
        <MetricTile label="High Risk" value={highRiskCount} tone={highRiskCount > 0 ? 'warning' : 'default'} />
      </div>

      <div className="grid gap-3 rounded-lg border border-border bg-card p-4 md:grid-cols-3">
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Cluster ID</span>
          <input
            value={context.clusterId}
            onChange={(event) => setContext((current) => ({ ...current, clusterId: event.target.value }))}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Project ID</span>
          <input
            value={context.projectId}
            onChange={(event) => setContext((current) => ({ ...current, projectId: event.target.value }))}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </label>
        <label className="space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Namespace</span>
          <input
            value={context.namespace}
            onChange={(event) => setContext((current) => ({ ...current, namespace: event.target.value }))}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </label>
        {responseContext?.warnings?.length ? (
          <p className="text-xs text-muted-foreground md:col-span-3">{responseContext.warnings.join(' ')}</p>
        ) : null}
      </div>

      <DataTable
        data={permissions}
        columns={permissionColumns}
        keyExtractor={(row) => `${row.resource}:${row.verb}`}
        searchPlaceholder="Search effective permissions..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No effective permissions found"
        pageSize={25}
      />

      <DataTable
        data={bindings}
        columns={bindingColumns}
        keyExtractor={(row) => row.bindingId || `${row.scope}:${row.roleId}:${row.roleName}`}
        searchPlaceholder="Search permission sources..."
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        emptyMessage="No role bindings contribute permissions"
        pageSize={10}
      />
    </div>
  );
}

function MetricTile({ label, value, tone = 'default' }: { label: string; value: number; tone?: 'default' | 'warning' }) {
  return (
    <div className="rounded-lg border border-border bg-card px-4 py-3">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className={cn('mt-1 text-2xl font-semibold tabular-nums', tone === 'warning' ? 'text-status-warning' : 'text-foreground')}>
        {value}
      </p>
    </div>
  );
}

function sourceSummary(sources: EffectivePermissionSource[]): string {
  const labels = sources.map((source) => source.roleName || source.roleId || source.bindingId || source.scope || 'binding');
  return unique(labels).join(', ');
}

function targetSummary(sources: EffectivePermissionSource[]): string {
  const labels = sources.map((source) => {
    if (source.clusterId) return `cluster:${source.clusterId}`;
    if (source.projectId) return `project:${source.projectId}`;
    return source.scope || 'global';
  });
  return unique(labels).join(', ');
}

function bindingTarget(binding: EffectivePermissionBinding): string {
  if (binding.clusterId) return `cluster:${binding.clusterId}`;
  if (binding.projectId) return `project:${binding.projectId}`;
  return binding.scope || 'global';
}

function unique(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean)));
}

function isHighRiskGrant(grant: EffectivePermissionGrant): boolean {
  return riskSort(grant) >= 2;
}

function riskSort(grant: EffectivePermissionGrant): number {
  if (grant.resource === '*' || grant.verb === '*') return 3;
  if (grant.resource === 'secrets' && ['read', 'list', 'watch'].includes(grant.verb)) return 3;
  if (['delete', 'manage', 'exec', 'proxy', 'sync'].includes(grant.verb)) return 2;
  if (['create', 'update', 'scale', 'restart'].includes(grant.verb)) return 1;
  return 0;
}

function riskLabel(grant: EffectivePermissionGrant): string {
  const risk = riskSort(grant);
  if (risk >= 3) return 'Critical';
  if (risk === 2) return 'High';
  if (risk === 1) return 'Medium';
  return 'Low';
}

function riskClassName(grant: EffectivePermissionGrant): string {
  const risk = riskSort(grant);
  if (risk >= 3) return 'bg-status-error/10 text-status-error';
  if (risk === 2) return 'bg-status-warning/10 text-status-warning';
  if (risk === 1) return 'bg-status-info/10 text-status-info';
  return 'bg-muted text-muted-foreground';
}

// ============================================================
// Create User Modal
// ============================================================

function CreateUserModal({
  globalRoles,
  onClose,
}: {
  globalRoles: GlobalRole[];
  onClose: () => void;
}) {
  const createUser = useCreateUser();
  const [showPassword, setShowPassword] = useState(false);
  const [form, setForm] = useState({
    username: '',
    email: '',
    displayName: '',
    password: '',
    globalRoles: [] as string[],
  });

  const toggleRole = (roleName: string) => {
    setForm((f) => ({
      ...f,
      globalRoles: f.globalRoles.includes(roleName)
        ? f.globalRoles.filter((r) => r !== roleName)
        : [...f.globalRoles, roleName],
    }));
  };

  const handleSave = async () => {
    if (!form.username || !form.email || !form.password) {
      toastError('Username, email, and password are required');
      return;
    }
    if (form.password.length < 8) {
      toastError('Password must be at least 8 characters');
      return;
    }

    try {
      await createUser.mutateAsync({
        username: form.username,
        email: form.email,
        displayName: form.displayName || form.username,
        password: form.password,
        globalRoles: form.globalRoles,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create User</h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Username</label>
              <input
                type="text"
                value={form.username}
                onChange={(e) => setForm((f) => ({ ...f, username: e.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, '') }))}
                placeholder="johndoe"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Display Name</label>
              <input
                type="text"
                value={form.displayName}
                onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
                placeholder="John Doe"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Email</label>
            <input
              type="email"
              value={form.email}
              onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
              placeholder="john@example.com"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Password</label>
            <div className="relative">
              <input
                type={showPassword ? 'text' : 'password'}
                value={form.password}
                onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                placeholder="Minimum 8 characters"
                className="w-full h-9 px-3 pr-10 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
              >
                {showPassword ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              </button>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Global Roles</label>
            <div className="flex flex-wrap gap-1.5">
              {globalRoles.map((role) => (
                <button
                  key={role.name}
                  onClick={() => toggleRole(role.name)}
                  className={cn(
                    'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                    form.globalRoles.includes(role.name)
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {role.displayName}
                </button>
              ))}
              {globalRoles.length === 0 && (
                <span className="text-xs text-muted-foreground">No roles available</span>
              )}
            </div>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createUser.isPending || !form.username || !form.email || !form.password}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createUser.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create User
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

// ============================================================
// Edit User Modal
// ============================================================

function EditUserModal({
  user,
  globalRoles,
  onClose,
}: {
  user: User;
  globalRoles: GlobalRole[];
  onClose: () => void;
}) {
  const updateUser = useUpdateUser();
  const [form, setForm] = useState({
    displayName: user.displayName,
    email: user.email,
    enabled: user.enabled,
    globalRoles: [...user.globalRoles],
  });

  const toggleRole = (roleName: string) => {
    setForm((f) => ({
      ...f,
      globalRoles: f.globalRoles.includes(roleName)
        ? f.globalRoles.filter((r) => r !== roleName)
        : [...f.globalRoles, roleName],
    }));
  };

  const handleSave = async () => {
    try {
      await updateUser.mutateAsync({
        id: user.id,
        data: {
          displayName: form.displayName,
          email: form.email,
          enabled: form.enabled,
          globalRoles: form.globalRoles,
        },
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Edit User</h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          {/* User info */}
          <div className="flex items-center gap-3 p-3 rounded-lg bg-muted/50 border border-border">
            <div className="w-10 h-10 rounded-full bg-gradient-to-br from-zinc-600 to-zinc-800 flex items-center justify-center flex-shrink-0">
              <span className="text-sm font-medium text-zinc-300">
                {(user.displayName || user.username).charAt(0).toUpperCase()}
              </span>
            </div>
            <div>
              <p className="font-medium text-foreground">{user.username}</p>
              <p className="text-xs text-muted-foreground capitalize">Provider: {user.provider}</p>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Display Name</label>
              <input
                type="text"
                value={form.displayName}
                onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Email</label>
              <input
                type="email"
                value={form.email}
                onChange={(e) => setForm((f) => ({ ...f, email: e.target.value }))}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Global Roles</label>
            <div className="flex flex-wrap gap-1.5">
              {globalRoles.map((role) => (
                <button
                  key={role.name}
                  onClick={() => toggleRole(role.name)}
                  className={cn(
                    'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                    form.globalRoles.includes(role.name)
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {role.displayName}
                </button>
              ))}
              {globalRoles.length === 0 && (
                <span className="text-xs text-muted-foreground">No roles available</span>
              )}
            </div>
          </div>

          <div className="rounded-lg border border-border p-4">
            <label className="flex items-center gap-3 cursor-pointer">
              <button
                onClick={() => setForm((f) => ({ ...f, enabled: !f.enabled }))}
                className={cn(
                  'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
                  form.enabled ? 'bg-primary' : 'bg-muted'
                )}
              >
                <span
                  className={cn(
                    'inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
                    form.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'
                  )}
                />
              </button>
              <div>
                <p className="text-sm font-medium text-foreground">
                  Account {form.enabled ? 'Active' : 'Inactive'}
                </p>
                <p className="text-xs text-muted-foreground">
                  {form.enabled
                    ? 'User can log in and access the platform'
                    : 'User is blocked from logging in'}
                </p>
              </div>
            </label>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={updateUser.isPending}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {updateUser.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Update User
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

// ============================================================
// Reset Password Result Modal
// ============================================================

function ResetPasswordResultModal({
  password,
  onClose,
}: {
  password: string;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [showPassword, setShowPassword] = useState(false);

  const handleCopy = async () => {
    const success = await copyToClipboard(password);
    if (success) {
      setCopied(true);
      toastSuccess('Password copied to clipboard');
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <h3 className="text-lg font-semibold text-foreground">Password Reset</h3>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          <p className="text-sm text-muted-foreground">
            A temporary password has been generated. Please share it securely with the user.
            They will be prompted to change it on next login.
          </p>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Temporary Password</label>
            <div className="flex items-center gap-2">
              <div className="relative flex-1">
                <input
                  type={showPassword ? 'text' : 'password'}
                  value={password}
                  readOnly
                  className="w-full h-9 px-3 pr-10 rounded-md border border-border bg-background text-sm font-mono
                    text-foreground focus:outline-none"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                >
                  {showPassword ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                </button>
              </div>
              <button
                onClick={handleCopy}
                className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border
                  text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                <Copy className="h-3.5 w-3.5" />
                {copied ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>

          <div className="rounded-lg border border-status-warning/20 bg-status-warning/5 p-3">
            <p className="text-xs text-status-warning">
              This password will not be shown again. Make sure to copy it before closing this dialog.
            </p>
          </div>
        </div>

        <div className="flex items-center justify-end px-6 py-4 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity"
          >
            Done
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}
