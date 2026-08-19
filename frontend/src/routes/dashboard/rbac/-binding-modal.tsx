import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { toastError } from '@/lib/toast';
import { useAppForm, useStore } from '@/lib/form';
import { useClusterRoles, useClusters, useCreateClusterRoleBinding, useUsers } from '@/lib/hooks';
import { isValidNamespace } from './-utils';

export function CreateClusterBindingModal({ onClose }: { onClose: () => void }) {
  const { data: usersData } = useUsers();
  const { data: clusterRoles } = useClusterRoles();
  const { data: clustersData } = useClusters();
  const createBinding = useCreateClusterRoleBinding();

  const users = usersData?.data || [];
  const clusters = clustersData?.data || [];
  const roles = clusterRoles || [];

  const form = useAppForm({
    defaultValues: { userId: '', roleId: '', clusterId: '', namespace: '' },
    validators: {
      // Old pre-submit check, ported 1:1 as a form-level onSubmit validator.
      onSubmit: ({ value }) =>
        !value.userId || !value.roleId || !value.clusterId || !isValidNamespace(value.namespace.trim())
          ? 'Select a user, cluster role, and cluster; namespace must be a valid label'
          : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        await createBinding.mutateAsync({
          user_id: value.userId,
          role_id: value.roleId,
          cluster_id: value.clusterId,
          namespace: value.namespace.trim() || undefined,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const namespaceValid = useStore(form.store, (s) => isValidNamespace(s.values.namespace.trim()));
  // Old disabled gate, recomputed from form state 1:1.
  const canSubmit = useStore(
    form.store,
    (s) =>
      !!s.values.userId &&
      !!s.values.roleId &&
      !!s.values.clusterId &&
      isValidNamespace(s.values.namespace.trim()),
  );

  return (
    <ModalShell
      title="Create Cluster Binding"
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            loading={createBinding.isPending}
            disabled={!canSubmit}
            onClick={() => void form.handleSubmit()}
          >
            Create Binding
          </ActionButton>
        </>
      }
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">User</label>
        <form.Field name="userId">
          {(field) => (
            <Select
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            >
              <option value="">Select a user…</option>
              {users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.displayName || u.username}
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Cluster Role</label>
        <form.Field name="roleId">
          {(field) => (
            <Select
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            >
              <option value="">Select a cluster role…</option>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.displayName || r.name}
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Cluster</label>
        <form.Field name="clusterId">
          {(field) => (
            <Select
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            >
              <option value="">Select a cluster…</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Namespace</label>
        <form.Field name="namespace">
          {(field) => (
            <Input
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="leave blank for cluster-wide"
              className={cn('font-mono', namespaceValid ? undefined : 'border-status-error')}
            />
          )}
        </form.Field>
        {!namespaceValid && (
          <p className="text-xs text-status-error">
            Must be a valid Kubernetes namespace (lowercase alphanumeric and dashes, ≤63 chars).
          </p>
        )}
      </div>
    </ModalShell>
  );
}
