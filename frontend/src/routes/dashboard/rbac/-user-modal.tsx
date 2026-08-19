import { useState } from 'react';
import { Copy, Eye, EyeOff } from 'lucide-react';
import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { cn, copyToClipboard } from '@/lib/utils';
import { toastError, toastSuccess } from '@/lib/toast';
import { useAppForm, useStore } from '@/lib/form';
import { useCreateUser, useUpdateUser } from '@/lib/hooks';
import type { GlobalRole, User } from '@/types';

export function CreateUserModal({
  globalRoles,
  onClose,
}: {
  globalRoles: GlobalRole[];
  onClose: () => void;
}) {
  const createUser = useCreateUser();
  const [showPassword, setShowPassword] = useState(false);
  const form = useAppForm({
    defaultValues: {
      username: '',
      email: '',
      displayName: '',
      password: '',
      globalRoles: [] as string[],
    },
    validators: {
      // Old pre-submit checks, ported 1:1 (same messages, same order).
      onSubmit: ({ value }) =>
        !value.username || !value.email || !value.password
          ? 'Username, email, and password are required'
          : value.password.length < 8
            ? 'Password must be at least 8 characters'
            : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        await createUser.mutateAsync({
          username: value.username,
          email: value.email,
          displayName: value.displayName || value.username,
          password: value.password,
          globalRoles: value.globalRoles,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const selectedRoles = useStore(form.store, (s) => s.values.globalRoles);
  const gate = useStore(
    form.store,
    (s) => !s.values.username || !s.values.email || !s.values.password,
  );

  const toggleRole = (roleName: string) => {
    form.setFieldValue(
      'globalRoles',
      selectedRoles.includes(roleName)
        ? selectedRoles.filter((r) => r !== roleName)
        : [...selectedRoles, roleName],
    );
  };

  return (
    <ModalShell
      title="Create User"
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            loading={createUser.isPending}
            disabled={gate}
            onClick={() => void form.handleSubmit()}
          >
            Create User
          </ActionButton>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Username</label>
          <form.Field name="username">
            {(field) => (
              <Input
                type="text"
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(e.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, ''))
                }
                onBlur={field.handleBlur}
                placeholder="johndoe"
                autoFocus
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Display Name</label>
          <form.Field name="displayName">
            {(field) => (
              <Input
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="John Doe"
              />
            )}
          </form.Field>
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Email</label>
        <form.Field name="email">
          {(field) => (
            <Input
              type="email"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="john@example.com"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Password</label>
        <div className="relative">
          <form.Field name="password">
            {(field) => (
              <Input
                type={showPassword ? 'text' : 'password'}
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Minimum 8 characters"
                className="pr-10"
              />
            )}
          </form.Field>
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
                selectedRoles.includes(role.name)
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:text-foreground',
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
    </ModalShell>
  );
}

export function EditUserModal({
  user,
  globalRoles,
  onClose,
}: {
  user: User;
  globalRoles: GlobalRole[];
  onClose: () => void;
}) {
  const updateUser = useUpdateUser();
  const form = useAppForm({
    defaultValues: {
      displayName: user.displayName,
      email: user.email,
      enabled: user.enabled,
      globalRoles: [...(user.globalRoles ?? [])],
    },
    onSubmit: async ({ value }) => {
      try {
        await updateUser.mutateAsync({
          id: user.id,
          data: {
            displayName: value.displayName,
            email: value.email,
            enabled: value.enabled,
            globalRoles: value.globalRoles,
          },
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const selectedRoles = useStore(form.store, (s) => s.values.globalRoles);
  const enabled = useStore(form.store, (s) => s.values.enabled);

  const toggleRole = (roleName: string) => {
    form.setFieldValue(
      'globalRoles',
      selectedRoles.includes(roleName)
        ? selectedRoles.filter((r) => r !== roleName)
        : [...selectedRoles, roleName],
    );
  };

  return (
    <ModalShell
      title="Edit User"
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            loading={updateUser.isPending}
            onClick={() => void form.handleSubmit()}
          >
            Update User
          </ActionButton>
        </>
      }
    >
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
          <form.Field name="displayName">
            {(field) => (
              <Input
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Email</label>
          <form.Field name="email">
            {(field) => (
              <Input
                type="email"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
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
                selectedRoles.includes(role.name)
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:text-foreground',
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
          <Switch checked={enabled} onCheckedChange={(checked) => form.setFieldValue('enabled', checked)} />
          <div>
            <p className="text-sm font-medium text-foreground">
              Account {enabled ? 'Active' : 'Inactive'}
            </p>
            <p className="text-xs text-muted-foreground">
              {enabled
                ? 'User can log in and access the platform'
                : 'User is blocked from logging in'}
            </p>
          </div>
        </label>
      </div>
    </ModalShell>
  );
}

export function ResetPasswordResultModal({
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
    <ModalShell
      title="Password Reset"
      onClose={onClose}
      size="sm"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <ActionButton intent="primary" onClick={onClose}>
          Done
        </ActionButton>
      }
    >
      <p className="text-sm text-muted-foreground">
        A temporary password has been generated. Please share it securely with the user.
        They will be prompted to change it on next login.
      </p>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Temporary Password</label>
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <Input
              type={showPassword ? 'text' : 'password'}
              value={password}
              readOnly
              className="pr-10 font-mono"
            />
            <button
              type="button"
              onClick={() => setShowPassword(!showPassword)}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
            >
              {showPassword ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
            </button>
          </div>
          <ActionButton icon={<Copy className="h-3.5 w-3.5" />} onClick={() => void handleCopy()}>
            {copied ? 'Copied' : 'Copy'}
          </ActionButton>
        </div>
      </div>

      <div className="rounded-lg border border-status-warning/20 bg-status-warning/5 p-3">
        <p className="text-xs text-status-warning">
          This password will not be shown again. Make sure to copy it before closing this dialog.
        </p>
      </div>
    </ModalShell>
  );
}
