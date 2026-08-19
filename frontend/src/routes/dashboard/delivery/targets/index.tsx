import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Crosshair, Plus } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  ErrorMessage,
  RedirectDeliveryList,
  inputClass,
  primaryButton,
  secondaryButton,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  createDeliveryTarget,
  listComponentBundles,
  listComponentBundleVersions,
  listDeliveryTargets,
  type DeliveryTarget,
  type DeliveryTargetRequest,
  type DriftPolicy,
} from "@/lib/api/delivery";
import {
  placementFromForm,
  placementHasSelector,
} from "@/components/delivery/target-form";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useRouter } from "@/lib/navigation";
import { formatRelativeTime } from "@/lib/utils";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { toastSuccess } from "@/lib/toast";

export function TargetsPage() {
  const { projectId, projects, projectQuery } = useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_targets", "list", scope);
  const canCreate = can(user, "delivery_targets", "create", scope);
  const [pageIndex, setPageIndex] = useDeliveryPageIndex();
  const [creating, setCreating] = useState(false);
  const pageSize = 25;
  const params = { limit: pageSize, offset: pageIndex * pageSize };
  const router = useRouter();
  const query = useQuery({
    queryKey: queryKeys.delivery.targets(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliveryTargets(projectId, params);
    },
    enabled: Boolean(projectId && allowed),
    refetchInterval: liveFallback(20_000),
  });
  useLiveQueryInvalidation(
    "delivery_target.changed",
    projectId
      ? queryKeys.delivery.targetsAll(projectId)
      : queryKeys.delivery.all,
  );
  const columns: Column<DeliveryTarget>[] = [
    {
      key: "name",
      header: "Target",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Crosshair className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium">{row.name}</p>
            <p className="text-xs text-muted-foreground">
              generation {row.generation}
            </p>
          </div>
        </div>
      ),
    },
    {
      key: "bundle",
      header: "Bundle version",
      accessor: (row) => <code className="text-xs">{row.bundleVersionId}</code>,
    },
    {
      key: "placement",
      header: "Placement",
      accessor: (row) =>
        row.placement.allClusters ? (
          <span className="text-status-warning">All project clusters</span>
        ) : (
          `${row.placement.clusterIds?.length ?? 0} explicit · ${Object.keys(row.placement.matchLabels ?? {}).length} labels · ${row.placement.clusterGroupIds?.length ?? 0} groups`
        ),
    },
    {
      key: "approval",
      header: "Approval",
      accessor: (row) =>
        row.rolloutPolicy.approvalRequired ? "Required" : "Policy controlled",
    },
    {
      key: "state",
      header: "State",
      accessor: (row) => (
        <DeliveryPhaseBadge
          value={
            row.deletionState !== "active"
              ? row.deletionState
              : row.suspended
                ? "suspended"
                : "active"
          }
        />
      ),
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
        permission="delivery_targets:list"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <PageHeader
            title="Targets"
            description="Bind an immutable bundle version to centrally evaluated placement and rollout policy."
            actions={
              canCreate ? (
                <button
                  type="button"
                  className={primaryButton}
                  onClick={() => setCreating(true)}
                >
                  <Plus className="h-4 w-4" /> New target
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
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyMessage="No delivery targets in this project"
            onRowClick={(row) =>
              router.push(
                `/dashboard/delivery/targets/${row.id}?project=${encodeURIComponent(projectId)}`,
              )
            }
            serverSide={{
              rowCount: query.data?.count ?? 0,
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
            }}
          />
        </PageShell>
      </DeliveryProjectGate>
      {creating && (
        <CreateTargetDialog
          projectId={projectId}
          onClose={() => setCreating(false)}
        />
      )}
    </>
  );
}

function CreateTargetDialog({
  projectId,
  onClose,
}: {
  projectId: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const router = useRouter();
  const [bundleId, setBundleId] = useState("");
  const [allClusters, setAllClusters] = useState(false);
  const [drift, setDrift] = useState<DriftPolicy>("repair");
  const [formError, setFormError] = useState<Error | null>(null);
  const bundles = useQuery({
    queryKey: queryKeys.delivery.bundles(projectId, { limit: 200 }),
    queryFn: () => listComponentBundles(projectId, { limit: 200 }),
  });
  const versions = useQuery({
    queryKey: queryKeys.delivery.bundleVersions(projectId, bundleId, {
      limit: 200,
    }),
    queryFn: () =>
      listComponentBundleVersions(projectId, bundleId, { limit: 200 }),
    enabled: Boolean(bundleId),
  });
  const mutation = useMutation({
    mutationFn: (body: DeliveryTargetRequest) =>
      createDeliveryTarget(body, crypto.randomUUID()),
    onSuccess: ({ data }) => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.targetsAll(projectId),
      });
      toastSuccess("Delivery target created");
      onClose();
      router.push(
        `/dashboard/delivery/targets/${data.id}?project=${encodeURIComponent(projectId)}`,
      );
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    try {
      const form = new FormData(event.currentTarget);
      const placement = placementFromForm(form, allClusters);
      if (!placementHasSelector(placement))
        throw new Error(
          "Select at least one explicit cluster, group, label, or expression. Empty placement selects nothing.",
        );
      const maintenanceText = String(form.get("maintenance") ?? "").trim();
      const maintenance = maintenanceText
        ? (JSON.parse(maintenanceText) as Record<string, unknown>)
        : {};
      mutation.mutate({
        project_id: projectId,
        name: String(form.get("name")).trim(),
        description: String(form.get("description") ?? "").trim() || undefined,
        bundle_version_id: String(form.get("bundle_version_id")),
        placement,
        rollout_policy: {
          approval_required: form.get("approval_required") === "on",
        },
        reconciliation_policy: {
          interval: String(form.get("interval")),
          retry_interval: String(form.get("retry_interval")),
          timeout: String(form.get("timeout")),
          prune: form.get("prune") === "on",
          wait: form.get("wait") === "on",
          drift,
        },
        maintenance_window_policy: maintenance,
        suspended: form.get("suspended") === "on",
      });
    } catch (error) {
      setFormError(
        error instanceof Error ? error : new Error("Target policy is invalid."),
      );
    }
  };
  return (
    <ModalShell
      title="Create delivery target"
      size="xl"
      onClose={onClose}
      subtitle="Placement is evaluated only by the management plane. Preview and launch remain separate operations."
    >
      <form className="space-y-5" onSubmit={submit}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name">
            <input
              name="name"
              required
              maxLength={128}
              className={inputClass}
            />
          </Field>
          <Field label="Description">
            <input name="description" maxLength={4096} className={inputClass} />
          </Field>
          <Field label="Bundle">
            <select
              value={bundleId}
              onChange={(e) => setBundleId(e.target.value)}
              required
              className={inputClass}
            >
              <option value="">Select bundle</option>
              {bundles.data?.data.map((bundle) => (
                <option key={bundle.id} value={bundle.id}>
                  {bundle.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Ready, verified bundle version">
            <select
              name="bundle_version_id"
              required
              disabled={!bundleId}
              className={inputClass}
            >
              <option value="">Select version</option>
              {versions.data?.data
                .filter(
                  (version) =>
                    version.state === "ready" &&
                    version.verificationStatus === "verified",
                )
                .map((version) => (
                  <option key={version.id} value={version.id}>
                    {version.version} · {version.resolvedRevision}
                  </option>
                ))}
            </select>
          </Field>
        </div>
        <fieldset className="space-y-4 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Placement selector
          </legend>
          <label className="flex items-center gap-2 text-sm font-medium text-status-warning">
            <input
              type="checkbox"
              checked={allClusters}
              onChange={(e) => setAllClusters(e.target.checked)}
            />{" "}
            Select every eligible cluster in this project
          </label>
          {!allClusters && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Explicit cluster IDs (comma-separated)">
                <textarea name="cluster_ids" className={textareaClass} />
              </Field>
              <Field label="Cluster group IDs (comma-separated)">
                <textarea name="group_ids" className={textareaClass} />
              </Field>
              <Field label="Match labels (one key=value per line)">
                <textarea name="labels" className={textareaClass} />
              </Field>
              <Field label="Expressions (one: key Operator value1,value2)">
                <textarea
                  name="expressions"
                  className={textareaClass}
                  placeholder="environment In production,staging&#10;gpu DoesNotExist"
                />
              </Field>
            </div>
          )}
          <Field label="Exclude cluster IDs (comma-separated)">
            <textarea name="exclude_ids" className={textareaClass} />
          </Field>
        </fieldset>
        <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">Reconciliation</legend>
          <Field label="Interval">
            <input
              name="interval"
              required
              defaultValue="10m"
              className={inputClass}
            />
          </Field>
          <Field label="Retry interval">
            <input
              name="retry_interval"
              required
              defaultValue="1m"
              className={inputClass}
            />
          </Field>
          <Field label="Timeout">
            <input
              name="timeout"
              required
              defaultValue="10m"
              className={inputClass}
            />
          </Field>
          <Field label="Drift">
            <select
              value={drift}
              onChange={(e) => setDrift(e.target.value as DriftPolicy)}
              className={inputClass}
            >
              <option value="repair">Detect and repair</option>
              <option value="detect">Detect only</option>
              <option value="ignore">Ignore</option>
            </select>
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input name="prune" type="checkbox" defaultChecked /> Prune removed
            objects
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input name="wait" type="checkbox" defaultChecked /> Wait for health
          </label>
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Maintenance policy (JSON object)">
            <textarea
              name="maintenance"
              className={textareaClass}
              placeholder="{}"
            />
          </Field>
          <div className="space-y-3 pt-6">
            <label className="flex items-center gap-2 text-sm">
              <input name="approval_required" type="checkbox" /> Require human
              rollout approval
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input name="suspended" type="checkbox" /> Create suspended
            </label>
          </div>
        </div>
        {(formError || mutation.isError) && (
          <ErrorMessage error={formError ?? mutation.error} />
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
            {mutation.isPending ? "Creating…" : "Create target"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1.5 text-sm">
      <span className="font-medium">{label}</span>
      {children}
    </label>
  );
}
export const Route = createFileRoute("/dashboard/delivery/targets/")({
  component: function DeliveryTargetsRedirect() {
    return <RedirectDeliveryList tab="targets" />;
  },
});
