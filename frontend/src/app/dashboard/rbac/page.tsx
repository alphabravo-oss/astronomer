'use client';

import { useState } from 'react';
import {
  useGlobalRoles,
  useClusterRoles,
  useProjectRoles,
  useUsers,
  useRoleBindings,
  useCreateUser,
  useUpdateUser,
  useDeleteUser,
  useResetUserPassword,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { RoleEditor } from '@/components/rbac/role-editor';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { GlobalRole, ClusterRole, ProjectRole, User, RoleBinding } from '@/types';
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
} from 'lucide-react';
import { toast } from 'sonner';
import { copyToClipboard } from '@/lib/utils';

type TabKey = 'global-roles' | 'cluster-roles' | 'project-roles' | 'users' | 'bindings';

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'global-roles', label: 'Global Roles', icon: Shield },
  { key: 'cluster-roles', label: 'Cluster Roles', icon: Lock },
  { key: 'project-roles', label: 'Project Roles', icon: Key },
  { key: 'users', label: 'Users', icon: Users },
  { key: 'bindings', label: 'Bindings', icon: Shield },
];

export default function RBACPage() {
  const [activeTab, setActiveTab] = useState<TabKey>('global-roles');
  const [showRoleEditor, setShowRoleEditor] = useState(false);
  const [showCreateUser, setShowCreateUser] = useState(false);
  const [editingUser, setEditingUser] = useState<User | null>(null);
  const [resetPasswordResult, setResetPasswordResult] = useState<{ userId: string; password: string } | null>(null);

  const { data: globalRoles, isLoading: globalLoading } = useGlobalRoles();
  const { data: clusterRoles, isLoading: clusterLoading } = useClusterRoles();
  const { data: projectRoles, isLoading: projectLoading } = useProjectRoles();
  const { data: usersData, isLoading: usersLoading } = useUsers();
  const { data: bindings, isLoading: bindingsLoading } = useRoleBindings();

  const deleteUser = useDeleteUser();
  const resetPassword = useResetUserPassword();

  const users = usersData?.data || [];

  const handleDeleteUser = (user: User) => {
    if (!confirm(`Delete user "${user.displayName || user.username}"? This action cannot be undone.`)) return;
    deleteUser.mutate(user.id);
  };

  const handleResetPassword = async (user: User) => {
    if (!confirm(`Reset password for "${user.displayName || user.username}"? A new temporary password will be generated.`)) return;
    try {
      const result = await resetPassword.mutateAsync(user.id);
      setResetPasswordResult({ userId: user.id, password: result.temporaryPassword });
    } catch {
      // Error handled by mutation
    }
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
      accessor: (row) => <StatusBadge status={row.enabled ? 'active' : 'disconnected'} label={row.enabled ? 'Enabled' : 'Disabled'} />,
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

  const bindingColumns: Column<RoleBinding>[] = [
    {
      key: 'name',
      header: 'Binding',
      accessor: (row) => <span className="font-medium text-foreground">{row.name}</span>,
    },
    {
      key: 'roleType',
      header: 'Scope',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">{row.roleType}</span>
      ),
    },
    {
      key: 'role',
      header: 'Role',
      accessor: (row) => <span className="font-mono text-xs text-muted-foreground">{row.roleName}</span>,
    },
    {
      key: 'subjects',
      header: 'Subjects',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.subjects.map((s, i) => (
            <span key={i} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              {s.kind}: {s.name}
            </span>
          ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
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
            emptyMessage="No role bindings found"
          />
        )}
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

      {/* Reset Password Result */}
      {resetPasswordResult && (
        <ResetPasswordResultModal
          password={resetPasswordResult.password}
          onClose={() => setResetPasswordResult(null)}
        />
      )}
    </div>
  );
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
      toast.error('Username, email, and password are required');
      return;
    }
    if (form.password.length < 8) {
      toast.error('Password must be at least 8 characters');
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
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create User</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
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
    </div>
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
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Edit User</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
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
    </div>
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
      toast.success('Password copied to clipboard');
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-md rounded-xl border border-border bg-popover shadow-2xl">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <h3 className="text-lg font-semibold text-foreground">Password Reset</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
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
    </div>
  );
}
