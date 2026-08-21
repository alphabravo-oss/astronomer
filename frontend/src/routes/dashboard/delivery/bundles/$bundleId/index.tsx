import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowLeft,
  GitCommitHorizontal,
  Plus,
  ShieldCheck,
} from "lucide-react";
import { load as parseYaml } from "js-yaml";
import { Link } from "@/lib/link";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  deliveryPageRowCount,
  inputClass,
  primaryButton,
  secondaryButton,
  textareaClass,
  RedirectDeliveryDetail,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
  withProjectQuery,
} from "@/components/delivery/shared";
import {
  createComponentBundleVersion,
  getComponentBundle,
  listComponentBundleVersions,
  listDeliverySources,
  type BundleScope,
  type ComponentBundleVersion,
  type CreateBundleVersionRequest,
  type DriftPolicy,
  type RendererKind,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can, isSuperuser } from "@/lib/permissions";
import { useParams } from "@/lib/navigation";
import { formatRelativeTime } from "@/lib/utils";
import { toastSuccess } from "@/lib/toast";

export function BundleDetailPage() {
  const { bundleId } = useParams<{ bundleId: string }>();
  const { projectId, projects, projectQuery, setProjectId, listHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_bundles", "read", {
    type: "project",
    id: projectId,
  });
  const canCreate = can(user, "delivery_bundles", "create", {
    type: "project",
    id: projectId,
  });
  const canCreatePlatformVersion =
    isSuperuser(user) && can(user, "delivery_platform", "update");
  const [pageIndex, setPageIndex] = useDeliveryPageIndex("version_page");
  const [creating, setCreating] = useState(false);
  const pageSize = 25;
  const bundle = useQuery({
    queryKey: queryKeys.delivery.bundle(projectId, bundleId),
    queryFn: () => getComponentBundle(projectId, bundleId),
    enabled: Boolean(projectId && bundleId && allowed),
  });
  const versions = useQuery({
    queryKey: queryKeys.delivery.bundleVersions(projectId, bundleId, {
      limit: pageSize,
      offset: pageIndex * pageSize,
    }),
    queryFn: () =>
      listComponentBundleVersions(projectId, bundleId, {
        limit: pageSize,
        offset: pageIndex * pageSize,
      }),
    enabled: Boolean(projectId && bundleId && allowed),
  });
  const columns: Column<ComponentBundleVersion>[] = [
    {
      key: "version",
      header: "Version",
      accessor: (row) => (
        <div>
          <p className="font-medium">{row.version}</p>
          <p className="font-mono text-xs text-muted-foreground">{row.id}</p>
        </div>
      ),
    },
    {
      key: "renderer",
      header: "Renderer / scope",
      accessor: (row) => `${row.renderer} · ${row.scope}`,
    },
    {
      key: "revision",
      header: "Immutable revision",
      accessor: (row) => (
        <div>
          <p className="max-w-56 truncate font-mono text-xs">
            {row.resolvedRevision || row.requestedRevision}
          </p>
          <p className="max-w-56 truncate font-mono text-[10px] text-muted-foreground">
            {row.artifactDigest || "resolution pending"}
          </p>
        </div>
      ),
    },
    {
      key: "verification",
      header: "Verification",
      accessor: (row) => (
        <div className="space-y-1">
          <DeliveryPhaseBadge value={row.verificationStatus} />
          {row.verificationIdentity && (
            <p className="max-w-48 truncate text-xs text-muted-foreground">
              {row.verificationIdentity}
            </p>
          )}
        </div>
      ),
    },
    {
      key: "state",
      header: "State",
      accessor: (row) => <DeliveryPhaseBadge value={row.state} />,
    },
    {
      key: "created",
      header: "Created",
      accessor: (row) => formatRelativeTime(row.createdAt),
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
        permission="delivery_bundles:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <Link
            href={withProjectQuery(listHref("bundles"), projectId)}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" /> Bundles
          </Link>
          <PageHeader
            eyebrow="Immutable delivery content"
            title={bundle.data?.name ?? "Bundle"}
            description={
              bundle.data?.description || "Reusable component definition"
            }
            actions={
              canCreate ? (
                <button
                  type="button"
                  className={primaryButton}
                  onClick={() => setCreating(true)}
                >
                  <Plus className="h-4 w-4" /> Add version
                </button>
              ) : undefined
            }
          />
          {bundle.data && (
            <DetailGrid>
              <Detail label="Bundle ID" value={bundle.data.id} mono />
              <Detail label="Project ID" value={bundle.data.projectId} mono />
              <Detail
                label="Created"
                value={new Date(bundle.data.createdAt).toLocaleString()}
              />
              <Detail
                label="Updated"
                value={new Date(bundle.data.updatedAt).toLocaleString()}
              />
            </DetailGrid>
          )}
          <PageSection
            title="Versions"
            description="A version is append-only. Mutable source references are resolved and verified before it can be targeted."
          >
            <DataTable
              data={versions.data?.data ?? []}
              columns={columns}
              keyExtractor={(row) => row.id}
              loading={versions.isLoading}
              isError={versions.isError}
              onRetry={() => void versions.refetch()}
              searchable={false}
              emptyMessage="No versions yet"
              serverSide={{
                rowCount: deliveryPageRowCount(
                  versions.data,
                  pageIndex,
                  pageSize,
                ),
                pagination: { pageIndex, pageSize },
                onPaginationChange: (next) => setPageIndex(next.pageIndex),
              }}
            />
          </PageSection>
        </PageShell>
      </DeliveryProjectGate>
      {creating && (
        <CreateVersionDialog
          projectId={projectId}
          bundleId={bundleId}
          canCreatePlatformVersion={canCreatePlatformVersion}
          onClose={() => setCreating(false)}
        />
      )}
    </DeliveryShell>
  );
}

function CreateVersionDialog({
  projectId,
  bundleId,
  canCreatePlatformVersion,
  onClose,
}: {
  projectId: string;
  bundleId: string;
  canCreatePlatformVersion: boolean;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [renderer, setRenderer] = useState<RendererKind>("helm");
  const [scope, setScope] = useState<BundleScope>("namespace");
  const [drift, setDrift] = useState<DriftPolicy>("repair");
  const [formError, setFormError] = useState<Error | null>(null);
  const sources = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, { limit: 200 }),
    queryFn: () => listDeliverySources(projectId, { limit: 200 }),
  });
  const mutation = useMutation({
    mutationFn: (body: CreateBundleVersionRequest) =>
      createComponentBundleVersion(bundleId, body, crypto.randomUUID()),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.bundleVersions(projectId, bundleId),
      });
      toastSuccess("Bundle version created and queued for resolution");
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      setFormError(null);
      const form = new FormData(event.currentTarget);
      const valuesText = String(form.get("values") ?? "").trim();
      const parsedValues = valuesText ? parseYaml(valuesText) : undefined;
      if (
        parsedValues !== undefined &&
        (parsedValues === null ||
          typeof parsedValues !== "object" ||
          Array.isArray(parsedValues))
      )
        throw new Error("Helm values must be a YAML mapping.");
      const capabilities = String(form.get("capabilities") ?? "")
        .split("\n")
        .map((line) => line.trim())
        .filter(Boolean)
        .map((line) => {
          const [name, ...constraint] = line.split(/\s+/);
          return { name, constraint: constraint.join(" ") || undefined };
        });
      const rendererSpec =
        renderer === "helm"
          ? {
              kind: "helm" as const,
              helm: {
                chart: String(form.get("chart")),
                chart_version: String(form.get("chart_version")),
                release_name: String(form.get("release_name")),
                target_namespace: String(form.get("target_namespace")),
                values: parsedValues as Record<string, unknown> | undefined,
                install_retries: Number(form.get("install_retries")),
                upgrade_retries: Number(form.get("upgrade_retries")),
                test: form.get("test") === "on",
              },
            }
          : {
              kind: "kustomize" as const,
              kustomize: {
                path: String(form.get("path")),
                target_namespace: String(form.get("target_namespace")),
                patches: String(form.get("patches") ?? "")
                  .split("\n---\n")
                  .map((item) => item.trim())
                  .filter(Boolean),
              },
            };
      mutation.mutate({
        project_id: projectId,
        version: String(form.get("version")).trim(),
        spec: {
          source_id: String(form.get("source_id")),
          requested_revision: String(form.get("requested_revision")).trim(),
          renderer: rendererSpec,
          scope,
          reconciliation_policy: {
            interval: String(form.get("interval")),
            retry_interval: String(form.get("retry_interval")),
            timeout: String(form.get("timeout")),
            prune: form.get("prune") === "on",
            wait: form.get("wait") === "on",
            drift,
          },
          required_capabilities: capabilities,
        },
        dependency_bundle_ids: String(form.get("dependencies") ?? "")
          .split(",")
          .map((id) => id.trim())
          .filter(Boolean),
      });
    } catch (error) {
      setFormError(
        error instanceof Error
          ? error
          : new Error("Bundle values are invalid."),
      );
    }
  };
  return (
    <ModalShell
      title="Add immutable bundle version"
      size="xl"
      onClose={onClose}
      titleIcon={<GitCommitHorizontal className="h-5 w-5" />}
      subtitle="The management plane resolves the requested reference to an immutable commit or digest and verifies trust before use."
    >
      <form className="space-y-5" onSubmit={submit}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Version label">
            <input name="version" required className={inputClass} />
          </Field>
          <Field label="Source">
            <select name="source_id" required className={inputClass}>
              <option value="">Select source</option>
              {sources.data?.data.map((source) => (
                <option key={source.id} value={source.id}>
                  {source.name} · {source.type}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Requested revision">
            <input
              name="requested_revision"
              required
              className={inputClass}
              placeholder="tag, branch, chart version, or digest"
            />
          </Field>
          <Field label="Renderer">
            <select
              value={renderer}
              onChange={(e) => setRenderer(e.target.value as RendererKind)}
              className={inputClass}
            >
              <option value="helm">Helm</option>
              <option value="kustomize">Kustomize</option>
            </select>
          </Field>
          <Field label="Scope">
            <select
              value={scope}
              onChange={(e) => setScope(e.target.value as BundleScope)}
              className={inputClass}
            >
              <option value="namespace">Namespace</option>
              {canCreatePlatformVersion && (
                <option value="platform">
                  Platform (superuser + approval)
                </option>
              )}
            </select>
          </Field>
        </div>
        <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-2">
          <legend className="px-1 text-sm font-medium">
            {renderer === "helm" ? "Helm renderer" : "Kustomize renderer"}
          </legend>
          {renderer === "helm" ? (
            <>
              <Field label="Chart">
                <input name="chart" required className={inputClass} />
              </Field>
              <Field label="Chart version">
                <input name="chart_version" required className={inputClass} />
              </Field>
              <Field label="Release name">
                <input name="release_name" required className={inputClass} />
              </Field>
            </>
          ) : (
            <Field label="Repository path">
              <input
                name="path"
                required
                defaultValue="./"
                className={inputClass}
              />
            </Field>
          )}
          <Field label="Target namespace">
            <input name="target_namespace" required className={inputClass} />
          </Field>
          {renderer === "helm" ? (
            <>
              <Field label="Install retries">
                <input
                  name="install_retries"
                  required
                  type="number"
                  min={0}
                  max={255}
                  defaultValue={3}
                  className={inputClass}
                />
              </Field>
              <Field label="Upgrade retries">
                <input
                  name="upgrade_retries"
                  required
                  type="number"
                  min={0}
                  max={255}
                  defaultValue={2}
                  className={inputClass}
                />
              </Field>
              <label className="flex items-center gap-2 text-sm">
                <input name="test" type="checkbox" defaultChecked /> Run chart
                tests
              </label>
              <div className="sm:col-span-2">
                <Field label="Values (YAML mapping)">
                  <textarea
                    name="values"
                    className={textareaClass}
                    spellCheck={false}
                  />
                </Field>
              </div>
            </>
          ) : (
            <div className="sm:col-span-2">
              <Field label="Patches (separate documents with ---)">
                <textarea
                  name="patches"
                  className={textareaClass}
                  spellCheck={false}
                />
              </Field>
            </div>
          )}
        </fieldset>
        <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">
            Reconciliation policy
          </legend>
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
            resources
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input name="wait" type="checkbox" defaultChecked /> Wait for health
          </label>
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Required capabilities (one name and optional constraint per line)">
            <textarea name="capabilities" className={textareaClass} />
          </Field>
          <Field label="Dependency bundle IDs (comma-separated)">
            <textarea name="dependencies" className={textareaClass} />
          </Field>
        </div>
        {scope === "platform" && (
          <p className="flex items-center gap-2 rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm text-status-warning">
            <ShieldCheck className="h-4 w-4" /> Platform scope requires
            superuser authorization and a human rollout approval.
          </p>
        )}
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
            {mutation.isPending ? "Creating…" : "Create immutable version"}
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
function DeliveryBundleDetailRedirect() {
  const { bundleId } = useParams<{ bundleId: string }>();
  return (
    <RedirectDeliveryDetail tab="bundles" id={bundleId}>
      <BundleDetailPage />
    </RedirectDeliveryDetail>
  );
}

export const Route = createFileRoute("/dashboard/delivery/bundles/$bundleId/")({
  component: DeliveryBundleDetailRedirect,
});
