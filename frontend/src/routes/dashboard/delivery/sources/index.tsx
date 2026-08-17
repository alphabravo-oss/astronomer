import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  ErrorMessage,
  inputClass,
  primaryButton,
  secondaryButton,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryProjectScope,
} from "@/components/delivery/shared";
import {
  createDeliverySource,
  deleteDeliverySource,
  listDeliverySources,
  rotateDeliverySourceCredential,
  verifyDeliverySource,
  type CreateDeliverySourceRequest,
  type DeliveryAuthMode,
  type DeliverySource,
  type DeliverySourceType,
  type SignatureProvider,
  type SourceCredentialInput,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { formatRelativeTime } from "@/lib/utils";
import { toastSuccess } from "@/lib/toast";
import { useRouter, useSearchParams } from "@/lib/navigation";

const sourceStatuses = ["pending", "ready", "degraded", "revoked"] as const;

function SourcesPage() {
  const { projectId, projects, projectQuery, setProjectId } =
    useDeliveryProjectScope();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const canList = can(user, "delivery_sources", "list", scope);
  const canCreate = can(user, "delivery_sources", "create", scope);
  const canUpdate = can(user, "delivery_sources", "update", scope);
  const canDelete = can(user, "delivery_sources", "delete", scope);
  const search = useSearchParams();
  const router = useRouter();
  const requestedStatus = search.get("status") ?? "";
  const status = sourceStatuses.includes(
    requestedStatus as (typeof sourceStatuses)[number],
  )
    ? requestedStatus
    : undefined;
  const [pageIndex, setPageIndex] = useDeliveryPageIndex();
  const [createOpen, setCreateOpen] = useState(false);
  const [verifySource, setVerifySource] = useState<DeliverySource | null>(null);
  const [rotateSource, setRotateSource] = useState<DeliverySource | null>(null);
  const [deleteSource, setDeleteSource] = useState<DeliverySource | null>(null);
  const pageSize = 25;
  const params = {
    limit: pageSize,
    offset: pageIndex * pageSize,
    ...(status ? { status } : {}),
  };
  const setStatus = (nextStatus: string) => {
    const next = new URLSearchParams(search);
    if (nextStatus) next.set("status", nextStatus);
    else next.delete("status");
    next.delete("page");
    router.replace(
      `/dashboard/delivery/sources${next.size ? `?${next.toString()}` : ""}`,
    );
  };
  const query = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliverySources(projectId, params);
    },
    enabled: Boolean(projectId && canList),
    refetchInterval: liveFallback(30_000),
  });
  useLiveQueryInvalidation(
    "delivery_source.changed",
    projectId
      ? queryKeys.delivery.sourcesAll(projectId)
      : queryKeys.delivery.all,
  );

  const columns: Column<DeliverySource>[] = [
    {
      key: "name",
      header: "Source",
      accessor: (row) => (
        <div>
          <p className="font-medium">{row.name}</p>
          <p className="max-w-72 truncate text-xs text-muted-foreground">
            {row.url}
          </p>
        </div>
      ),
    },
    {
      key: "type",
      header: "Kind",
      accessor: (row) => row.type.replaceAll("_", " "),
    },
    {
      key: "auth",
      header: "Authentication",
      accessor: (row) => (
        <span>
          {row.authMode.replaceAll("_", " ")}
          {row.credential.configured ? " · configured" : ""}
        </span>
      ),
    },
    {
      key: "trust",
      header: "Trust",
      accessor: (row) =>
        row.trustPolicy.allowUnsigned ? (
          <span className="text-status-warning">Unsigned allowed</span>
        ) : (
          <span className="inline-flex items-center gap-1">
            <ShieldCheck className="h-4 w-4 text-status-success" />{" "}
            {row.trustPolicy.provider}
          </span>
        ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => <DeliveryPhaseBadge value={row.status} />,
    },
    {
      key: "updated",
      header: "Last checked",
      accessor: (row) =>
        row.lastResolvedAt ? formatRelativeTime(row.lastResolvedAt) : "Never",
    },
    {
      key: "actions",
      header: "",
      sortable: false,
      accessor: (row) => (
        <div className="flex justify-end gap-1">
          {canUpdate && (
            <button
              type="button"
              className={secondaryButton}
              onClick={() => setVerifySource(row)}
              aria-label={`Verify ${row.name}`}
            >
              <RefreshCw className="h-4 w-4" /> Verify
            </button>
          )}
          {canUpdate &&
            row.authMode !== "none" &&
            row.authMode !== "workload_identity" && (
              <button
                type="button"
                className={secondaryButton}
                onClick={() => setRotateSource(row)}
                aria-label={`Rotate credentials for ${row.name}`}
              >
                <KeyRound className="h-4 w-4" />
              </button>
            )}
          {canDelete && (
            <button
              type="button"
              className={secondaryButton}
              onClick={() => setDeleteSource(row)}
              aria-label={`Delete ${row.name}`}
            >
              <Trash2 className="h-4 w-4 text-status-error" />
            </button>
          )}
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
        permission="delivery_sources:list"
        allowed={canList}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <PageHeader
            eyebrow="Continuous Delivery"
            title="Sources"
            description="Reusable authenticated and verified Git, OCI, and Helm locations. Credentials are write-only."
            actions={
              canCreate ? (
                <button
                  type="button"
                  className={primaryButton}
                  onClick={() => setCreateOpen(true)}
                >
                  <Plus className="h-4 w-4" /> Add source
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
            emptyMessage="No delivery sources in this project"
            toolbar={
              <select
                aria-label="Source status"
                value={status ?? ""}
                onChange={(event) => setStatus(event.target.value)}
                className={inputClass}
              >
                <option value="">All statuses</option>
                {sourceStatuses.map((value) => (
                  <option key={value} value={value}>
                    {value.replaceAll("_", " ")}
                  </option>
                ))}
              </select>
            }
            serverSide={{
              rowCount: query.data?.count ?? 0,
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
            }}
          />
        </PageShell>
      </DeliveryProjectGate>
      {createOpen && (
        <SourceCreateDialog
          projectId={projectId}
          onClose={() => setCreateOpen(false)}
        />
      )}
      {verifySource && (
        <SourceVerifyDialog
          projectId={projectId}
          source={verifySource}
          onClose={() => setVerifySource(null)}
        />
      )}
      {rotateSource && (
        <CredentialDialog
          projectId={projectId}
          source={rotateSource}
          onClose={() => setRotateSource(null)}
        />
      )}
      <SourceDeleteDialog
        projectId={projectId}
        source={deleteSource}
        onClose={() => setDeleteSource(null)}
      />
    </DeliveryShell>
  );
}

function SourceCreateDialog({
  projectId,
  onClose,
}: {
  projectId: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [kind, setKind] = useState<DeliverySourceType>("git");
  const [authMode, setAuthMode] = useState<DeliveryAuthMode>("none");
  const [allowUnsigned, setAllowUnsigned] = useState(false);
  const [provider, setProvider] = useState<SignatureProvider>("git");
  const mutation = useMutation({
    mutationFn: (body: CreateDeliverySourceRequest) =>
      createDeliverySource(body, crypto.randomUUID()),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Delivery source created");
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const credential = credentialFromForm(form, authMode);
    const body: CreateDeliverySourceRequest = {
      project_id: projectId,
      name: String(form.get("name") ?? "").trim(),
      description: String(form.get("description") ?? "").trim() || undefined,
      type: kind,
      url: String(form.get("url") ?? "").trim(),
      auth_mode: authMode,
      credential: Object.keys(credential).length ? credential : undefined,
      ca_bundle: String(form.get("ca_bundle") ?? "").trim() || undefined,
      proxy_ref: String(form.get("proxy_ref") ?? "").trim() || undefined,
      trust_policy: allowUnsigned
        ? { allow_unsigned: true }
        : {
            allow_unsigned: false,
            provider,
            identity: String(form.get("identity") ?? "").trim() || undefined,
            issuer: String(form.get("issuer") ?? "").trim() || undefined,
            key_ref: String(form.get("key_ref") ?? "").trim() || undefined,
          },
    };
    mutation.mutate(body);
  };
  return (
    <ModalShell
      title="Add delivery source"
      size="lg"
      onClose={onClose}
      subtitle="Secret fields are encrypted on submit and are never returned to this browser."
    >
      <form className="space-y-4" onSubmit={submit}>
        <Field label="Name">
          <input name="name" required maxLength={128} className={inputClass} />
        </Field>
        <Field label="Description">
          <textarea
            name="description"
            maxLength={4096}
            className={textareaClass}
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Source kind">
            <select
              value={kind}
              onChange={(e) => {
                const next = e.target.value as DeliverySourceType;
                setKind(next);
                setProvider(next === "git" ? "git" : "cosign_keyless");
                if (next !== "git" && authMode === "ssh") setAuthMode("none");
              }}
              className={inputClass}
            >
              {sourceKinds.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Authentication">
            <select
              value={authMode}
              onChange={(e) => setAuthMode(e.target.value as DeliveryAuthMode)}
              className={inputClass}
            >
              {authModesFor(kind).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <Field label="URL">
          <input
            name="url"
            required
            type="url"
            className={inputClass}
            placeholder={
              kind === "git"
                ? "https://github.example/team/repo.git"
                : kind === "helm_http"
                  ? "https://charts.example.com"
                  : "oci://registry.example.com/team/artifact"
            }
          />
        </Field>
        <CredentialFields mode={authMode} />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Enterprise CA bundle (optional)">
            <textarea
              name="ca_bundle"
              className={textareaClass}
              placeholder="PEM certificate chain"
            />
          </Field>
          <Field label="Registered proxy reference (optional)">
            <input name="proxy_ref" className={inputClass} />
          </Field>
        </div>
        <fieldset className="space-y-3 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Supply-chain trust
          </legend>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={allowUnsigned}
              onChange={(e) => setAllowUnsigned(e.target.checked)}
            />{" "}
            Allow unsigned content
          </label>
          {!allowUnsigned && (
            <>
              <Field label="Verification provider">
                <select
                  value={provider}
                  onChange={(e) =>
                    setProvider(e.target.value as SignatureProvider)
                  }
                  className={inputClass}
                >
                  <option value="git">Git signature</option>
                  <option value="cosign_key">Cosign public key</option>
                  <option value="cosign_keyless">Cosign keyless</option>
                </select>
              </Field>
              <div className="grid gap-4 sm:grid-cols-2">
                {provider === "git" && (
                  <Field label="Trusted identity (optional)">
                    <input name="identity" className={inputClass} />
                  </Field>
                )}
                {provider === "cosign_keyless" && (
                  <Field label="Trusted identity">
                    <input name="identity" required className={inputClass} />
                  </Field>
                )}
                {provider === "cosign_keyless" && (
                  <Field label="OIDC issuer">
                    <input
                      name="issuer"
                      required
                      type="url"
                      className={inputClass}
                    />
                  </Field>
                )}
                {(provider === "git" || provider === "cosign_key") && (
                  <Field label="Registered public key reference">
                    <input
                      name="key_ref"
                      required
                      className={inputClass}
                      placeholder="team-signing-key"
                    />
                  </Field>
                )}
              </div>
            </>
          )}
        </fieldset>
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
            {mutation.isPending ? "Creating…" : "Create source"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function SourceVerifyDialog({
  projectId,
  source,
  onClose,
}: {
  projectId: string;
  source: DeliverySource;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (values: { revision: string; chart?: string }) =>
      verifyDeliverySource(
        source.id,
        {
          project_id: projectId,
          requested_revision: values.revision,
          chart: values.chart,
        },
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Source verification queued");
      onClose();
    },
  });
  return (
    <ModalShell title={`Verify ${source.name}`} onClose={onClose}>
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          const form = new FormData(event.currentTarget);
          mutation.mutate({
            revision: String(form.get("revision")),
            chart: String(form.get("chart") ?? "") || undefined,
          });
        }}
      >
        <Field label="Revision to resolve">
          <input
            name="revision"
            required
            className={inputClass}
            placeholder="branch, tag, version, or digest"
          />
        </Field>
        {(source.type === "helm_http" || source.type === "helm_oci") && (
          <Field label="Chart">
            <input name="chart" required className={inputClass} />
          </Field>
        )}
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
            {mutation.isPending ? "Queuing…" : "Verify immutable revision"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function CredentialDialog({
  projectId,
  source,
  onClose,
}: {
  projectId: string;
  source: DeliverySource;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (credential: SourceCredentialInput) =>
      rotateDeliverySourceCredential(
        source.id,
        { project_id: projectId, auth_mode: source.authMode, credential },
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Credential rotation started");
      onClose();
    },
  });
  return (
    <ModalShell
      title={`Rotate ${source.name} credentials`}
      onClose={onClose}
      subtitle="Old material is retained downstream until the new credential resolves the approved revision."
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate(
            credentialFromForm(
              new FormData(event.currentTarget),
              source.authMode,
            ),
          );
        }}
      >
        <CredentialFields mode={source.authMode} />
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
            Rotate credential
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function SourceDeleteDialog({
  projectId,
  source,
  onClose,
}: {
  projectId: string;
  source: DeliverySource | null;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: () =>
      source
        ? deleteDeliverySource(projectId, source.id, crypto.randomUUID())
        : Promise.resolve(),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Delivery source deleted");
      onClose();
    },
  });
  return (
    <ConfirmDialog
      open={Boolean(source)}
      onClose={onClose}
      onConfirm={() => mutation.mutate()}
      title="Delete delivery source"
      description={`Delete “${source?.name ?? ""}”? Sources referenced by bundle versions cannot be deleted.`}
      confirmValue={source?.name}
      variant="destructive"
      loading={mutation.isPending}
    />
  );
}

function CredentialFields({ mode }: { mode: DeliveryAuthMode }) {
  if (mode === "none" || mode === "workload_identity")
    return (
      <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground">
        {mode === "none"
          ? "No credential will be stored."
          : "Authentication is provided by the configured workload identity."}
      </p>
    );
  if (mode === "basic")
    return (
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Username">
          <input
            name="username"
            required
            autoComplete="off"
            className={inputClass}
          />
        </Field>
        <Field label="Password">
          <input
            name="password"
            required
            type="password"
            autoComplete="new-password"
            className={inputClass}
          />
        </Field>
      </div>
    );
  if (mode === "bearer")
    return (
      <Field label="Bearer token">
        <input
          name="token"
          required
          type="password"
          autoComplete="new-password"
          className={inputClass}
        />
      </Field>
    );
  return (
    <>
      <Field label="SSH private key">
        <textarea name="private_key" required className={textareaClass} />
      </Field>
      <Field label="Known hosts">
        <textarea name="known_hosts" required className={textareaClass} />
      </Field>
      <Field label="Key passphrase (optional)">
        <input
          name="passphrase"
          type="password"
          autoComplete="new-password"
          className={inputClass}
        />
      </Field>
    </>
  );
}

function credentialFromForm(
  form: FormData,
  mode: DeliveryAuthMode,
): SourceCredentialInput {
  const value = (key: string) => String(form.get(key) ?? "");
  if (mode === "basic")
    return { username: value("username"), password: value("password") };
  if (mode === "bearer") return { token: value("token") };
  if (mode === "ssh")
    return {
      private_key: value("private_key"),
      known_hosts: value("known_hosts"),
      passphrase: value("passphrase") || undefined,
    };
  return {};
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
      <span className="font-medium text-foreground">{label}</span>
      {children}
    </label>
  );
}

const sourceKinds: Array<[DeliverySourceType, string]> = [
  ["git", "Git repository"],
  ["oci_artifact", "OCI artifact"],
  ["helm_http", "Helm HTTP repository"],
  ["helm_oci", "Helm OCI repository"],
];
function authModesFor(
  kind: DeliverySourceType,
): Array<[DeliveryAuthMode, string]> {
  const values: Array<[DeliveryAuthMode, string]> = [
    ["none", "Public / none"],
    ["basic", "Username and password"],
    ["bearer", "Bearer token"],
    ["workload_identity", "Workload identity"],
  ];
  if (kind === "git") values.splice(3, 0, ["ssh", "SSH key"]);
  return values;
}

export const Route = createFileRoute("/dashboard/delivery/sources/")({
  component: SourcesPage,
});
