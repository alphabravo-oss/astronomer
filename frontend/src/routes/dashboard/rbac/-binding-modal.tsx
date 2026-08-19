import { ModalShell } from '@/components/ui/modal-shell';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { cn } from '@/lib/utils';
import { toastError } from '@/lib/toast';
import { useAppForm, useStore } from '@/lib/form';
import {
  useClusterRoles,
  useClusters,
  useCreateAccessBinding,
  useGlobalRoles,
  useProjectRoles,
  useProjects,
  useUsers,
} from '@/lib/hooks';
import { clusterLabel, isValidNamespace, projectLabel, roleTitle, userLabel } from './-utils';

export function CreateClusterBindingModal({ onClose }: { onClose: () => void }) {
  const { data: usersData } = useUsers({ pageSize: 200 });
  const { data: globalRoles } = useGlobalRoles();
  const { data: clusterRoles } = useClusterRoles();
  const { data: projectRoles } = useProjectRoles();
  const { data: clustersData } = useClusters({ pageSize: 200 });
  const { data: projectsData } = useProjects({ pageSize: 200 });
  const createBinding = useCreateAccessBinding();

  const users = usersData?.data || [];
  const clusters = clustersData?.data || [];
  const projects = projectsData?.data || [];

  const form = useAppForm({
    defaultValues: {
      scope: 'cluster' as 'global' | 'cluster' | 'project',
      userId: '',
      roleId: '',
      clusterId: '',
      projectId: '',
      namespace: '',
    },
    validators: {
      onSubmit: ({ value }) => {
        if (!value.userId || !value.roleId) {
          return 'Select a user and a role';
        }
        if (value.scope === 'cluster' && (!value.clusterId || !isValidNamespace(value.namespace.trim()))) {
          return 'Select a cluster; namespace must be a valid label';
        }
        if (value.scope === 'project' && !value.projectId) {
          return 'Select a project';
        }
        return undefined;
      },
    },
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        await createBinding.mutateAsync({
          scope: value.scope,
          user_id: value.userId,
          role_id: value.roleId,
          cluster_id: value.clusterId || undefined,
          project_id: value.projectId || undefined,
          namespace: value.namespace.trim() || undefined,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const scope = useStore(form.store, (s) => s.values.scope);
  const namespaceValid = useStore(form.store, (s) => isValidNamespace(s.values.namespace.trim()));
  const canSubmit = useStore(form.store, (s) => {
    if (!s.values.userId || !s.values.roleId) return false;
    if (s.values.scope === 'cluster') {
      return !!s.values.clusterId && isValidNamespace(s.values.namespace.trim());
    }
    if (s.values.scope === 'project') return !!s.values.projectId;
    return true;
  });

  const roles =
    scope === 'global' ? globalRoles || [] : scope === 'project' ? projectRoles || [] : clusterRoles || [];

  return (
    <ModalShell
      title="Create Binding"
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
        <label className="text-sm font-medium text-foreground">Scope</label>
        <form.Field name="scope">
          {(field) => (
            <Select
              value={field.state.value}
              onChange={(e) => {
                field.handleChange(e.target.value as 'global' | 'cluster' | 'project');
                form.setFieldValue('roleId', '');
              }}
              onBlur={field.handleBlur}
            >
              <option value="global">Global</option>
              <option value="cluster">Cluster</option>
              <option value="project">Project</option>
            </Select>
          )}
        </form.Field>
      </div>

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
                  {userLabel(u)}
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Role</label>
        <form.Field name="roleId">
          {(field) => (
            <Select
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
            >
              <option value="">Select a {scope} role…</option>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>
                  {roleTitle(r)}
                </option>
              ))}
            </Select>
          )}
        </form.Field>
      </div>

      {scope === 'cluster' && (
        <>
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
                      {clusterLabel(c)}
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
        </>
      )}

      {scope === 'project' && (
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Project</label>
          <form.Field name="projectId">
            {(field) => (
              <Select
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              >
                <option value="">Select a project…</option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {projectLabel(p)}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </div>
      )}
    </ModalShell>
  );
}
