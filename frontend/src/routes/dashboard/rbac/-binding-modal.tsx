import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { PrincipalPicker } from "@/components/rbac/principal-picker";
import { RemoteClusterPicker } from "@/components/clusters/remote-cluster-picker";
import { cn } from "@/lib/utils";
import { toastError } from "@/lib/toast";
import { useAppForm, useStore } from "@/lib/form";
import {
  useClusterRoles,
  useApplyProjectRoleTemplate,
  useCreateAccessBinding,
  useGlobalRoles,
  useProjectRoles,
  useRoleTemplates,
} from "@/lib/hooks/rbac";
import { useProjects } from "@/lib/hooks/projects";
import { isValidNamespace, projectLabel, roleTitle } from "./-utils";

export function CreateClusterBindingModal({
  onClose, fixedScope,
}: {
  onClose: () => void; fixedScope?: { kind: "project"; projectId: string };
}) {
  const { data: globalRoles } = useGlobalRoles();
  const { data: clusterRoles } = useClusterRoles();
  const { data: projectRoles } = useProjectRoles();
  const { data: templates } = useRoleTemplates();
  const { data: projectsData } = useProjects({ pageSize: 200 });
  const createBinding = useCreateAccessBinding();
  const applyTemplate = useApplyProjectRoleTemplate();

  const projects = projectsData?.data || [];

  const form = useAppForm({
    defaultValues: {
      scope: (fixedScope ? "project" : "cluster") as "global" | "cluster" | "project",
      userId: "",
      roleId: "",
      clusterId: "",
      projectId: fixedScope?.projectId ?? "",
      namespace: "",
    },
    validators: {
      onSubmit: ({ value }) => {
        if (!value.userId || !value.roleId) {
          return "Select a user and a role";
        }
        if (
          value.scope === "cluster" &&
          (!value.clusterId || !isValidNamespace(value.namespace.trim()))
        ) {
          return "Select a cluster; namespace must be a valid label";
        }
        if (value.scope === "project" && !value.projectId) {
          return "Select a project";
        }
        return undefined;
      },
    },
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === "string");
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        if (value.scope === "project" && value.roleId.startsWith("template:")) {
          await applyTemplate.mutateAsync({
            projectId: value.projectId,
            templateName: value.roleId.slice("template:".length),
            userId: value.userId,
          });
        } else {
          await createBinding.mutateAsync({
            scope: value.scope,
            user_id: value.userId,
            role_id: value.roleId,
            cluster_id: value.clusterId || undefined,
            project_id: value.projectId || undefined,
            namespace: value.namespace.trim() || undefined,
          });
        }
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });

  const scope = useStore(form.store, (s) => s.values.scope);
  const namespaceValid = useStore(form.store, (s) =>
    isValidNamespace(s.values.namespace.trim()),
  );
  const canSubmit = useStore(form.store, (s) => {
    if (!s.values.userId || !s.values.roleId) return false;
    if (s.values.scope === "cluster") {
      return (
        !!s.values.clusterId && isValidNamespace(s.values.namespace.trim())
      );
    }
    if (s.values.scope === "project") return !!s.values.projectId;
    return true;
  });

  const roles =
    scope === "global"
      ? globalRoles || []
      : scope === "project"
        ? projectRoles || []
        : clusterRoles || [];

  return (
    <ModalShell
      title="Create Binding"
      onClose={onClose}
      size="md"
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            loading={createBinding.isPending || applyTemplate.isPending}
            disabled={!canSubmit}
          >
            Create Binding
          </ActionButton>
        </>
      }
    >
      <form.AppForm>
        <form.FormErrorSummary
          serverError={
            createBinding.error?.message || applyTemplate.error?.message
          }
        />
      </form.AppForm>
      {!fixedScope && <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-b53e75b3-111"
        >
          Scope
        </label>
        <form.Field name="scope">
          {(field) => (
            <Select
              id="field-b53e75b3-111"
              name={field.name}
              value={field.state.value}
              onChange={(e) => {
                field.handleChange(
                  e.target.value as "global" | "cluster" | "project",
                );
                form.setFieldValue("roleId", "");
              }}
              onBlur={field.handleBlur}
            >
              <option value="global">Global</option>
              <option value="cluster">Cluster</option>
              <option value="project">Project</option>
            </Select>
          )}
        </form.Field>
      </div>}

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-b53e75b3-131"
        >
          User
        </label>
        <form.Field name="userId">
          {(field) => (
            <PrincipalPicker
              id="field-b53e75b3-131"
              value={field.state.value}
              onChange={field.handleChange}
            />
          )}
        </form.Field>
        <p className="text-xs text-muted-foreground">
          External identities are verified with their provider before the
          binding is created. “Pending sign-in” access activates on first SSO
          login.
        </p>
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-b53e75b3-151"
        >
          Role
        </label>
        <form.Field name="roleId">
          {(field) => (
            <Select
              id="field-b53e75b3-151"
              name={field.name}
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
              {scope === "project" &&
                (templates ?? []).filter(
                  (template) => template.scope === "project",
                ).length > 0 && (
                  <optgroup label="Curated templates">
                    {(templates ?? [])
                      .filter((template) => template.scope === "project")
                      .map((template) => (
                        <option
                          key={template.name}
                          value={`template:${template.name}`}
                        >
                          {template.displayName} ({template.riskLevel} risk)
                        </option>
                      ))}
                  </optgroup>
                )}
            </Select>
          )}
        </form.Field>
      </div>

      {scope === "cluster" && (
        <>
          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="field-b53e75b3-173"
            >
              Cluster
            </label>
            <form.Field name="clusterId">
              {(field) => (
                <RemoteClusterPicker
                  id="field-b53e75b3-173"
                  name={field.name}
                  ariaLabel="Cluster"
                  value={field.state.value}
                  onChange={field.handleChange}
                  onBlur={field.handleBlur}
                  placeholder="Select a cluster…"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label htmlFor="field-b53e75b3-193">Namespace</label>
            <form.Field name="namespace">
              {(field) => (
                <Input
                  id="field-b53e75b3-193"
                  name={field.name}
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="leave blank for cluster-wide"
                  className={cn(
                    "font-mono",
                    namespaceValid ? undefined : "border-status-error",
                  )}
                />
              )}
            </form.Field>
            {!namespaceValid && (
              <p className="text-xs text-status-error">
                Must be a valid Kubernetes namespace (lowercase alphanumeric and
                dashes, ≤63 chars).
              </p>
            )}
          </div>
        </>
      )}

      {scope === "project" && !fixedScope && (
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-b53e75b3-217"
          >
            Project
          </label>
          <form.Field name="projectId">
            {(field) => (
              <Select
                id="field-b53e75b3-217"
                name={field.name}
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
