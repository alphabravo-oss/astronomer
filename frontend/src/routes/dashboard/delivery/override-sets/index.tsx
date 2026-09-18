import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ModalShell } from "@/components/ui/modal-shell";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import {
  DeliveryProjectGate,
  DeliveryShell,
  ErrorMessage,
  primaryButton,
  secondaryButton,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
  deliveryPageRowCount,
} from "@/components/delivery/shared";
import {
  createDeliveryOverrideSet,
  deleteDeliveryOverrideSet,
  listDeliveryOverrideSets,
  listDeliveryConfigurationTemplates,
  updateDeliveryOverrideSet,
  type DeliveryOverrideSet,
  type DeliveryOverrideSetWrite,
} from "@/lib/api/delivery";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { queryKeys } from "@/lib/query-keys";
import { formatRelativeTime } from "@/lib/utils";
import { toastSuccess } from "@/lib/toast";

const scopes = [
  "organization",
  "project",
  "environment",
  "group",
  "cluster",
  "rollout",
] as const;

function OverrideSetsPage() {
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
    DeliveryOverrideSet | null | undefined
  >();
  const pageSize = 20;
  const query = useQuery({
    queryKey: queryKeys.delivery.overrideSets(projectId, {
      limit: pageSize,
      offset: pageIndex * pageSize,
    }),
    queryFn: ({ signal }) =>
      listDeliveryOverrideSets(
        projectId,
        { limit: pageSize, offset: pageIndex * pageSize },
        signal,
      ),
    enabled: Boolean(projectId && allowed),
  });
  const client = useQueryClient();
  const remove = useMutation({
    mutationFn: (item: DeliveryOverrideSet) =>
      deleteDeliveryOverrideSet(projectId, item.id, item.generation),
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: queryKeys.delivery.overrideSetsAll(projectId),
      });
      toastSuccess("Override set deleted");
    },
  });
  const columns: Column<DeliveryOverrideSet>[] = [
    {
      key: "name",
      header: "Override",
      accessor: (row) => (
        <div>
          <p className="font-medium">{row.name}</p>
          <p className="text-xs text-muted-foreground">
            {row.enabled ? "Enabled" : "Disabled"}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    {
      key: "scope",
      header: "Scope",
      accessor: (row) => <span className="capitalize">{row.scope}</span>,
      sortAccessor: (row) => row.scope,
    },
    {
      key: "precedence",
      header: "Precedence",
      accessor: (row) => row.precedence,
      sortAccessor: (row) => row.precedence,
    },
    {
      key: "configuration",
      header: "Configuration",
      accessor: (row) =>
        `${Object.keys(row.values).length} values · ${(row.patches ?? []).length} patches`,
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
            className={secondaryButton}
            disabled={!canUpdate}
            onClick={() => setEditing(row)}
          >
            <Pencil className="h-4 w-4" /> Edit
          </button>
          <button
            className={secondaryButton}
            disabled={!canDelete || remove.isPending}
            onClick={() => {
              if (window.confirm(`Delete override set “${row.name}”?`))
                remove.mutate(row);
            }}
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
            eyebrow="Layered configuration"
            title="Configuration overrides"
            description="Apply deterministic organization-to-rollout value layers. Equal-precedence conflicts are rejected before a rollout can be planned."
            actions={
              canCreate ? (
                <button
                  className={primaryButton}
                  onClick={() => setEditing(null)}
                >
                  <Plus className="h-4 w-4" /> New override
                </button>
              ) : undefined
            }
          />
          <PageSection title="Override sets">
            <DataTable
              data={query.data?.data ?? []}
              columns={columns}
              keyExtractor={(row) => row.id}
              searchable
              searchPlaceholder="Search overrides…"
              loading={query.isLoading}
              isError={query.isError || remove.isError}
              onRetry={() => void query.refetch()}
              emptyMessage="No configuration overrides"
              serverSide={{
                rowCount: deliveryPageRowCount(query.data, pageIndex, pageSize),
                pagination: { pageIndex, pageSize },
                onPaginationChange: (next) => setPageIndex(next.pageIndex),
              }}
            />
          </PageSection>
        </PageShell>
      </DeliveryProjectGate>
      {editing !== undefined ? (
        <OverrideEditor
          projectId={projectId}
          item={editing}
          onClose={() => setEditing(undefined)}
        />
      ) : null}
    </DeliveryShell>
  );
}

function OverrideEditor({
  projectId,
  item,
  onClose,
}: {
  projectId: string;
  item: DeliveryOverrideSet | null;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [localError, setLocalError] = useState<unknown>();
  const templates = useQuery({
    queryKey: queryKeys.delivery.configurationTemplates(projectId, {
      limit: 200,
    }),
    queryFn: ({ signal }) =>
      listDeliveryConfigurationTemplates(projectId, { limit: 200 }, signal),
  });
  const mutation = useMutation({
    mutationFn: (body: DeliveryOverrideSetWrite) =>
      item
        ? updateDeliveryOverrideSet(projectId, item.id, item.generation, body)
        : createDeliveryOverrideSet(body),
    onSuccess: () => {
      void client.invalidateQueries({
        queryKey: queryKeys.delivery.overrideSetsAll(projectId),
      });
      toastSuccess(item ? "Override set updated" : "Override set created");
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setLocalError(undefined);
    const data = new FormData(event.currentTarget);
    try {
      const selectedScope = String(
        data.get("scope"),
      ) as DeliveryOverrideSetWrite["scope"];
      const scopeId = String(data.get("scopeId") ?? "").trim();
      if (!["organization", "project"].includes(selectedScope) && !scopeId)
        throw new Error("Scope ID is required for this scope");
      mutation.mutate({
        project_id: projectId,
        template_id: String(data.get("templateId") ?? "") || null,
        name: String(data.get("name") ?? "").trim(),
        scope: selectedScope,
        scope_id: scopeId || null,
        precedence: Number(data.get("precedence") ?? 0),
        values: JSON.parse(String(data.get("values") ?? "{}")) as Record<
          string,
          unknown
        >,
        patches: JSON.parse(String(data.get("patches") ?? "[]")) as string[],
        enabled: data.get("enabled") === "on",
      });
    } catch (error) {
      setLocalError(error);
    }
  };
  const inputClass =
    "h-9 w-full rounded-md border border-input bg-background px-3";
  return (
    <ModalShell
      title={item ? `Edit ${item.name}` : "New configuration override"}
      onClose={onClose}
    >
      <form onSubmit={submit} className="space-y-4">
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Name</span>
          <input
            name="name"
            required
            maxLength={128}
            defaultValue={item?.name}
            className={inputClass}
          />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Configuration template</span>
          <select
            name="templateId"
            defaultValue={item?.templateId ?? ""}
            className={inputClass}
          >
            <option value="">Any selected template</option>
            {templates.data?.data.map((template) => (
              <option key={template.id} value={template.id}>
                {template.name}
              </option>
            ))}
          </select>
          <span className="text-xs text-muted-foreground">
            When bound, this override can only be used with that template.
          </span>
        </label>
        <div className="grid gap-4 sm:grid-cols-3">
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Scope</span>
            <select
              name="scope"
              defaultValue={item?.scope ?? "project"}
              className={inputClass}
            >
              {scopes.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Scope ID</span>
            <input
              name="scopeId"
              defaultValue={item?.scopeId ?? ""}
              className={inputClass}
              placeholder="Required for specific scopes"
            />
          </label>
          <label className="space-y-1.5 text-sm">
            <span className="font-medium">Precedence</span>
            <input
              name="precedence"
              type="number"
              defaultValue={item?.precedence ?? 0}
              className={inputClass}
            />
          </label>
        </div>
        <p className="text-xs text-muted-foreground">
          Environment and group scopes use a cluster-group UUID; cluster uses a
          cluster UUID; rollout uses a delivery-target UUID. Organization and
          project scopes leave Scope ID empty.
        </p>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Values JSON</span>
          <textarea
            name="values"
            rows={10}
            required
            className={`${textareaClass} font-mono text-xs`}
            defaultValue={JSON.stringify(item?.values ?? {}, null, 2)}
          />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Patches JSON array</span>
          <textarea
            name="patches"
            rows={5}
            className={`${textareaClass} font-mono text-xs`}
            defaultValue={JSON.stringify(item?.patches ?? [], null, 2)}
          />
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            name="enabled"
            type="checkbox"
            defaultChecked={item?.enabled ?? true}
          />{" "}
          Enabled for resolution
        </label>
        {localError || mutation.isError ? (
          <ErrorMessage error={localError ?? mutation.error} />
        ) : null}
        <div className="flex justify-end gap-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={mutation.isPending}
          >
            Save override
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

export const Route = createFileRoute("/dashboard/delivery/override-sets/")({
  component: OverrideSetsPage,
});
