import { Select } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { FormShell } from "@/components/ui/form-shell";
import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { Field } from "@/components/form/fields";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  ErrorMessage,
  RedirectDeliveryList,
  deliveryPageRowCount,
  inputClass,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
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
  type SourceCredentialInput,
} from "@/lib/api/delivery-sources";
import type { SignatureProvider } from "@/lib/api/delivery-common";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { formatRelativeTime } from "@/lib/utils";
import { toastSuccess } from "@/lib/toast";
import { useNavigate, useLocation } from "@tanstack/react-router";

const sourceStatuses = ["pending", "ready", "degraded", "revoked"] as const;

export function SourcesPage() {
  const { projectId, projects, projectQuery, listHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const canList = can(user, "delivery_sources", "list", scope);
  const canCreate = can(user, "delivery_sources", "create", scope);
  const canUpdate = can(user, "delivery_sources", "update", scope);
  const canDelete = can(user, "delivery_sources", "delete", scope);
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
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
    void navigate({
      to: `${listHref("sources")}${next.size ? `?${next.toString()}` : ""}`,
      replace: true,
    });
  };
  const query = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliverySources(projectId, params, signal);
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
            <ActionButton
              onClick={() => setVerifySource(row)}
              aria-label={`Verify ${row.name}`}
              icon={<RefreshCw className="h-4 w-4" />}
            >
              Verify
            </ActionButton>
          )}
          {canUpdate &&
            row.authMode !== "none" &&
            row.authMode !== "workload_identity" && (
              <ActionButton
                onClick={() => setRotateSource(row)}
                aria-label={`Rotate credentials for ${row.name}`}
                size="icon"
                icon={<KeyRound className="h-4 w-4" />}
              />
            )}
          {canDelete && (
            <ActionButton
              onClick={() => setDeleteSource(row)}
              aria-label={`Delete ${row.name}`}
              size="icon"
              icon={<Trash2 className="h-4 w-4 text-status-error" />}
            />
          )}
        </div>
      ),
    },
  ];

  return (
    <>
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
            title="Sources"
            description="Reusable authenticated and verified Git, OCI, and Helm locations. Credentials are write-only."
            actions={
              canCreate ? (
                <ActionButton
                  onClick={() => setCreateOpen(true)}
                  intent="primary"
                  icon={<Plus className="h-4 w-4" />}
                >
                  Add source
                </ActionButton>
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
            permission="delivery_sources:list"
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyState={{
              title: "No delivery sources in this project",
              description:
                "Connect a Git or OCI source for your application's manifests.",
              action: canCreate
                ? { label: "Add source", onClick: () => setCreateOpen(true) }
                : undefined,
            }}
            toolbar={
              <Select
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
              </Select>
            }
            serverSide={{
              rowCount: deliveryPageRowCount(query.data),
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
    </>
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
      <FormShell className="space-y-4" onSubmit={submit}>
        <Field label="Name">
          <Input name="name" required maxLength={128} className={inputClass} />
        </Field>
        <Field label="Description">
          <Textarea
            name="description"
            maxLength={4096}
            className={textareaClass}
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Source kind">
            <Select
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
            </Select>
          </Field>
          <Field label="Authentication">
            <Select
              value={authMode}
              onChange={(e) => setAuthMode(e.target.value as DeliveryAuthMode)}
              className={inputClass}
            >
              {authModesFor(kind).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <Field label="URL">
          <Input
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
            <Textarea
              name="ca_bundle"
              className={textareaClass}
              placeholder="PEM certificate chain"
            />
          </Field>
          <Field label="Registered proxy reference (optional)">
            <Input name="proxy_ref" className={inputClass} />
          </Field>
        </div>
        <fieldset className="space-y-3 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Supply-chain trust
          </legend>
          <label className="flex items-center gap-2 text-sm">
            <Input
              type="checkbox"
              checked={allowUnsigned}
              onChange={(e) => setAllowUnsigned(e.target.checked)}
            />{" "}
            Allow unsigned content
          </label>
          {!allowUnsigned && (
            <>
              <Field label="Verification provider">
                <Select
                  value={provider}
                  onChange={(e) =>
                    setProvider(e.target.value as SignatureProvider)
                  }
                  className={inputClass}
                >
                  <option value="git">Git signature</option>
                  <option value="cosign_key">Cosign public key</option>
                  <option value="cosign_keyless">Cosign keyless</option>
                </Select>
              </Field>
              <div className="grid gap-4 sm:grid-cols-2">
                {provider === "git" && (
                  <Field label="Trusted identity (optional)">
                    <Input name="identity" className={inputClass} />
                  </Field>
                )}
                {provider === "cosign_keyless" && (
                  <Field label="Trusted identity">
                    <Input name="identity" required className={inputClass} />
                  </Field>
                )}
                {provider === "cosign_keyless" && (
                  <Field label="OIDC issuer">
                    <Input
                      name="issuer"
                      required
                      type="url"
                      className={inputClass}
                    />
                  </Field>
                )}
                {(provider === "git" || provider === "cosign_key") && (
                  <Field label="Registered public key reference">
                    <Input
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
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
            loadingLabel="Creating…"
          >
            Create source
          </ActionButton>
        </div>
      </FormShell>
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
      <FormShell
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
          <Input
            name="revision"
            required
            className={inputClass}
            placeholder="branch, tag, version, or digest"
          />
        </Field>
        {(source.type === "helm_http" || source.type === "helm_oci") && (
          <Field label="Chart">
            <Input name="chart" required className={inputClass} />
          </Field>
        )}
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
            loadingLabel="Queuing…"
          >
            Verify immutable revision
          </ActionButton>
        </div>
      </FormShell>
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
      <FormShell
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
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
          >
            Rotate credential
          </ActionButton>
        </div>
      </FormShell>
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
          <Input
            name="username"
            required
            autoComplete="off"
            className={inputClass}
          />
        </Field>
        <Field label="Password">
          <Input
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
        <Input
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
        <Textarea name="private_key" required className={textareaClass} />
      </Field>
      <Field label="Known hosts">
        <Textarea name="known_hosts" required className={textareaClass} />
      </Field>
      <Field label="Key passphrase (optional)">
        <Input
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
  validateSearch: (search: Record<string, unknown>) => {
    const project = typeof search.project === "string" ? search.project : null;
    const status = typeof search.status === "string" ? search.status : null;
    return {
      ...(project ? { project } : {}),
      ...(status ? { status } : {}),
    };
  },
  component: function DeliverySourcesRedirect() {
    return <RedirectDeliveryList tab="sources" />;
  },
});
