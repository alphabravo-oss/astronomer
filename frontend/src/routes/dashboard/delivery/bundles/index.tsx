import { pageTableCount } from "@/lib/api/pagination";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { FormShell } from "@/components/ui/form-shell";
import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, Plus } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import {
  DeliveryProjectGate,
  ErrorMessage,
  inputClass,
  primaryButton,
  secondaryButton,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  createComponentBundle,
  listComponentBundles,
  type ComponentBundle,
} from "@/lib/api/delivery-bundles";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { formatRelativeTime } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { toastSuccess } from "@/lib/toast";

export function BundlesPage() {
  const { projectId, projects, projectQuery, entityHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_bundles", "list", scope);
  const canCreate = can(user, "delivery_bundles", "create", scope);
  const navigate = useNavigate();
  const [pageIndex, setPageIndex] = useDeliveryPageIndex();
  const [creating, setCreating] = useState(false);
  const pageSize = 25;
  const params = { limit: pageSize, offset: pageIndex * pageSize };
  const query = useQuery({
    queryKey: queryKeys.delivery.bundles(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listComponentBundles(projectId, params, signal);
    },
    enabled: Boolean(projectId && allowed),
    refetchInterval: liveFallback(30_000),
  });
  useLiveQueryInvalidation(
    "component_bundle.changed",
    projectId
      ? queryKeys.delivery.bundlesAll(projectId)
      : queryKeys.delivery.all,
  );
  const columns: Column<ComponentBundle>[] = [
    {
      key: "name",
      header: "Bundle",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Boxes className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium">{row.name}</p>
            <p className="text-xs text-muted-foreground">
              {row.description || "No description"}
            </p>
          </div>
        </div>
      ),
    },
    {
      key: "id",
      header: "Stable ID",
      accessor: (row) => <code className="text-xs">{row.id}</code>,
    },
    {
      key: "updated",
      header: "Updated",
      accessor: (row) => formatRelativeTime(row.updatedAt),
    },
  ];
  return (
    <>
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_bundles:list"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <PageHeader
            title="Component bundles"
            description="Stable bundle identities with append-only, immutable and centrally verified versions."
            actions={
              canCreate ? (
                <button
                  className={primaryButton}
                  type="button"
                  onClick={() => setCreating(true)}
                >
                  <Plus className="h-4 w-4" /> New bundle
                </button>
              ) : undefined
            }
          />
          <DataTable
            data={query.data?.data ?? []}
            columns={columns}
            keyExtractor={(row) => row.id}
            loading={query.isLoading}
            isError={query.isError}
            error={query.error}
            permission="delivery_bundles:list"
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyState={{
              title: "No component bundles in this project",
              description:
                "Create a bundle to version and deliver your application components.",
              action: canCreate
                ? { label: "Create bundle", onClick: () => setCreating(true) }
                : undefined,
            }}
            onRowClick={(row) =>
              void navigate({ to: entityHref("bundles", row.id) })
            }
            serverSide={{
              ...pageTableCount(query.data),
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
            }}
          />
        </PageShell>
      </DeliveryProjectGate>
      {creating && (
        <CreateBundleDialog
          projectId={projectId}
          onClose={() => setCreating(false)}
        />
      )}
    </>
  );
}

function CreateBundleDialog({
  projectId,
  onClose,
}: {
  projectId: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const { entityHref } = useDeliveryWorkspace();
  const mutation = useMutation({
    mutationFn: (body: { name: string; description?: string }) =>
      createComponentBundle(projectId, body, crypto.randomUUID()),
    onSuccess: (bundle) => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.bundlesAll(projectId),
      });
      toastSuccess("Component bundle created");
      onClose();
      void navigate({ to: entityHref("bundles", bundle.id) });
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    mutation.mutate({
      name: String(form.get("name") ?? "").trim(),
      description: String(form.get("description") ?? "").trim() || undefined,
    });
  };
  return (
    <ModalShell
      title="Create component bundle"
      onClose={onClose}
      subtitle="Versions are immutable and added after the stable bundle is created."
    >
      <FormShell className="space-y-4" onSubmit={submit}>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Name</span>
          <Input name="name" required maxLength={128} className={inputClass} />
        </label>
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Description</span>
          <Textarea
            name="description"
            maxLength={4096}
            className={textareaClass}
          />
        </label>
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={mutation.isPending}
          >
            {mutation.isPending ? "Creating…" : "Create bundle"}
          </button>
        </div>
      </FormShell>
    </ModalShell>
  );
}

export const Route = createFileRoute("/dashboard/delivery/bundles/")({
  component: BundlesPage,
});
