import { pageTableCount } from "@/lib/api/pagination";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { FormShell } from "@/components/ui/form-shell";
import { Input } from "@/components/ui/input";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  DeliveryProjectGate,
  DeliveryShell,
  ErrorMessage,
  primaryButton,
  secondaryButton,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  createDeliveryConfigurationTemplate,
  deleteDeliveryConfigurationTemplate,
  listDeliveryConfigurationTemplates,
  updateDeliveryConfigurationTemplate,
  type DeliveryConfigurationTemplate,
} from "@/lib/api/delivery-configuration";
import type { RendererKind } from "@/lib/api/delivery-bundles";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { queryKeys } from "@/lib/query-keys";
import { formatRelativeTime } from "@/lib/utils";
import { toastSuccess } from "@/lib/toast";

export function ConfigurationTemplatesPage() {
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_configuration_templates", "read", scope);
  const canCreate = can(
    user,
    "delivery_configuration_templates",
    "create",
    scope,
  );
  const canUpdate = can(
    user,
    "delivery_configuration_templates",
    "update",
    scope,
  );
  const canDelete = can(
    user,
    "delivery_configuration_templates",
    "delete",
    scope,
  );
  const [pageIndex, setPageIndex] = useDeliveryPageIndex();
  const [editing, setEditing] = useState<
    DeliveryConfigurationTemplate | null | undefined
  >(undefined);
  const [deleting, setDeleting] =
    useState<DeliveryConfigurationTemplate | null>(null);
  const pageSize = 20;
  const query = useQuery({
    queryKey: queryKeys.delivery.configurationTemplates(projectId, {
      limit: pageSize,
      offset: pageIndex * pageSize,
    }),
    queryFn: ({ signal }) =>
      listDeliveryConfigurationTemplates(
        projectId,
        { limit: pageSize, offset: pageIndex * pageSize },
        signal,
      ),
    enabled: Boolean(projectId && allowed),
  });
  const client = useQueryClient();
  const remove = useMutation({
    mutationFn: (template: DeliveryConfigurationTemplate) =>
      deleteDeliveryConfigurationTemplate(
        projectId,
        template.id,
        template.generation,
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.configurationTemplatesAll(projectId),
      });
      toastSuccess("Configuration template deleted");
      setDeleting(null);
    },
  });
  const columns: Column<DeliveryConfigurationTemplate>[] = [
    {
      key: "name",
      header: "Template",
      accessor: (row) => (
        <div>
          <p className="font-medium">{row.name}</p>
          <p className="text-xs text-muted-foreground">
            {row.description || "No description"}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    { key: "renderer", header: "Renderer", accessor: (row) => row.renderer },
    {
      key: "layers",
      header: "Configuration",
      accessor: (row) =>
        `${Object.keys(row.values).length} values · ${row.patches.length} patches · ${row.secretRefs.length} Secret refs`,
    },
    {
      key: "updated",
      header: "Updated",
      accessor: (row) => formatRelativeTime(row.updatedAt),
      sortAccessor: (row) => row.updatedAt,
    },
    {
      key: "actions",
      header: "Actions",
      accessor: (row) => (
        <div className="flex gap-2">
          <button
            type="button"
            className={secondaryButton}
            disabled={!canUpdate}
            onClick={() => setEditing(row)}
          >
            <Pencil className="h-4 w-4" /> Edit
          </button>
          <button
            type="button"
            className={secondaryButton}
            disabled={!canDelete || remove.isPending}
            onClick={() => setDeleting(row)}
          >
            <Trash2 className="h-4 w-4" /> Delete
          </button>
        </div>
      ),
    },
  ];
  return (
    <DeliveryShell
      projectId={projectId}
      projects={projects}
      setProjectId={setProjectId}
    >
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_configuration_templates:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <PageHeader
            eyebrow="Reusable configuration"
            title="Configuration templates"
            description="Project-scoped Helm values or Kustomize patches. Helm templates can reference existing Kubernetes Secrets without exposing their values."
            actions={
              canCreate ? (
                <button
                  type="button"
                  className={primaryButton}
                  onClick={() => setEditing(null)}
                >
                  <Plus className="h-4 w-4" /> New template
                </button>
              ) : undefined
            }
          />
          <PageSection title="Templates">
            <DataTable
              data={query.data?.data ?? []}
              columns={columns}
              keyExtractor={(row) => row.id}
              searchable
              searchPlaceholder="Search templates…"
              loading={query.isLoading}
              isError={query.isError || remove.isError}
              onRetry={() => void query.refetch()}
              emptyState={{
                title: "No configuration templates",
                description: "Create a reusable values or patch template.",
              }}
              serverSide={{
                ...pageTableCount(query.data),
                pagination: { pageIndex, pageSize },
                onPaginationChange: (next) => setPageIndex(next.pageIndex),
              }}
            />
          </PageSection>
        </PageShell>
      </DeliveryProjectGate>
      {editing !== undefined && (
        <TemplateEditor
          projectId={projectId}
          template={editing}
          onClose={() => setEditing(undefined)}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        onConfirm={() => deleting && remove.mutate(deleting)}
        title="Delete configuration template?"
        description="Rollouts that reference this template may no longer resolve configuration."
        confirmText="Delete template"
        confirmValue={deleting?.name}
        variant="destructive"
        loading={remove.isPending}
      />
    </DeliveryShell>
  );
}

function TemplateEditor({
  projectId,
  template,
  onClose,
}: {
  projectId: string;
  template: DeliveryConfigurationTemplate | null;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [localError, setLocalError] = useState<Error | null>(null);
  const mutation = useMutation({
    mutationFn: (body: {
      name: string;
      description: string;
      renderer: RendererKind;
      values: Record<string, unknown>;
      patches: string[];
      secret_refs: Array<{ name: string; key: string; value_path: string }>;
    }) =>
      template
        ? updateDeliveryConfigurationTemplate(
            projectId,
            template.id,
            template.generation,
            { project_id: projectId, ...body },
          )
        : createDeliveryConfigurationTemplate({
            project_id: projectId,
            ...body,
          }),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.configurationTemplatesAll(projectId),
      });
      toastSuccess(
        template
          ? "Configuration template updated"
          : "Configuration template created",
      );
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setLocalError(null);
    try {
      const form = new FormData(event.currentTarget);
      const values = JSON.parse(String(form.get("values") ?? "{}"));
      const patches = JSON.parse(String(form.get("patches") ?? "[]"));
      const secretRefs = JSON.parse(String(form.get("secretRefs") ?? "[]"));
      if (!values || Array.isArray(values) || typeof values !== "object")
        throw new Error("Values must be one JSON object.");
      if (
        !Array.isArray(patches) ||
        !patches.every((item) => typeof item === "string")
      )
        throw new Error("Patches must be a JSON array of strings.");
      if (!Array.isArray(secretRefs))
        throw new Error("Secret references must be a JSON array.");
      mutation.mutate({
        name: String(form.get("name") ?? "").trim(),
        description: String(form.get("description") ?? "").trim(),
        renderer: String(form.get("renderer") ?? "helm") as RendererKind,
        values,
        patches,
        secret_refs: secretRefs,
      });
    } catch (error) {
      setLocalError(error instanceof Error ? error : new Error("Invalid JSON"));
    }
  };
  return (
    <ModalShell
      title={template ? `Edit ${template.name}` : "New configuration template"}
      size="xl"
      onClose={onClose}
      subtitle="Templates are generation-fenced. Inline password, token, credential, private-key, and API-key fields are rejected by the server."
    >
      <FormShell className="space-y-4" onSubmit={submit}>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Name</span>
          <Input
            name="name"
            required
            maxLength={128}
            defaultValue={template?.name}
          />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Description</span>
          <Input
            name="description"
            maxLength={4096}
            defaultValue={template?.description}
          />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Renderer</span>
          <Select name="renderer" defaultValue={template?.renderer ?? "helm"}>
            <option value="helm">Helm</option>
            <option value="kustomize">Kustomize</option>
          </Select>
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Values JSON</span>
          <Textarea
            name="values"
            required
            rows={10}
            className="font-mono text-xs"
            defaultValue={JSON.stringify(template?.values ?? {}, null, 2)}
          />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Kustomize patches JSON array</span>
          <Textarea
            name="patches"
            rows={6}
            className="font-mono text-xs"
            defaultValue={JSON.stringify(template?.patches ?? [], null, 2)}
          />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Secret references</span>
          <Textarea
            name="secretRefs"
            rows={5}
            className="font-mono text-xs"
            defaultValue={JSON.stringify(template?.secretRefs ?? [], null, 2)}
          />
          <span className="text-xs text-muted-foreground">
            Helm only. Each item requires name, key, and value_path. The Secret
            must already exist in the assignment Flux control namespace; its
            value is read directly by Flux and is never stored here.
          </span>
        </label>
        {(localError || mutation.isError) && (
          <ErrorMessage error={localError ?? mutation.error} />
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={mutation.isPending}
          >
            Save template
          </button>
        </div>
      </FormShell>
    </ModalShell>
  );
}
