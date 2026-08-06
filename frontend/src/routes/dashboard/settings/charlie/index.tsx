import { createFileRoute } from "@tanstack/react-router";
import {
  useEffect,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  Bot,
  CheckCircle2,
  Database,
  KeyRound,
  Loader2,
  Network,
  Plus,
  RefreshCw,
  Save,
  Shield,
  Sparkles,
  Trash2,
  Upload,
} from "lucide-react";
import { Link } from "@/lib/link";
import { useRouter, useSearchParams } from "@/lib/navigation";
import { useFeatureFlags } from "@/lib/hooks";
import { useAuthStore } from "@/lib/store";
import { cn, formatRelativeTime } from "@/lib/utils";
import {
  PermissionState,
  StatePanel,
} from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  CHARLIE_ADMIN_TABS,
  adjacentTab,
  automationValidationIssues,
  canManageCharlie,
  completeDiagnostics,
  mergeCharlieSearch,
  normalizeCharlieAdminTab,
  parseOnboardingFile,
  type CharlieAdminTab,
} from "@/components/charlie/admin-utils";
import {
  acknowledgeCharlieDisclosure,
  consumeCharlieOnboarding,
  deleteCharlieAutomationRule,
  disconnectCharlie,
  emergencyDisableCharlie,
  getCharlieAccess,
  getCharlieAgent,
  getCharlieAutomation,
  getCharlieConnection,
  getCharlieDiagnostics,
  getCharlieMode,
  listCharlieTriggerEvents,
  retryCharlieTriggerEvent,
  runCharlieAgentAction,
  uninstallCharlieAgent,
  updateCharlieAutomation,
  updateCharlieActionPolicy,
  updateCharlieMode,
  validateCharlieOnboarding,
  type CharlieAutomationView,
  type CharlieMode,
  type CharlieOnboardingInput,
  type CharlieOnboardingView,
  type CharlieTriggerRule,
  type CharlieTriggerEvent,
} from "@/lib/api/charlie-admin";

export const Route = createFileRoute("/dashboard/settings/charlie/")({
  component: CharlieAdminPage,
});

const tabLabels: Record<CharlieAdminTab, string> = {
  connection: "Connection",
  agent: "Agent",
  mode: "Mode",
  automation: "Automation",
  access: "Access",
  diagnostics: "Diagnostics",
};
const button =
  "inline-flex min-h-9 items-center justify-center gap-2 rounded-md border border-border px-3 py-2 text-sm font-medium transition-colors motion-reduce:transition-none hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50";
const primary = `${button} border-primary bg-primary text-primary-foreground hover:bg-primary/90`;
const field =
  "h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring";

function CharlieAdminPage() {
  return <CharlieAdminContent />;
}

export function CharlieAdminContent() {
  const flags = useFeatureFlags();
  const user = useAuthStore((s) => s.user);
  const params = useSearchParams();
  const router = useRouter();
  const requestedTab = normalizeCharlieAdminTab(params.get("tab"));
  if (flags.isError)
    return (
      <Unavailable
        name="Charlie feature state"
        retry={() => void flags.refetch()}
      />
    );
  if (flags.data?.["feature.charlie"] !== true && flags.data?.["feature.charlie"] !== false)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie settings"
      />
    );
  if (!canManageCharlie(user))
    return (
      <PermissionState
        title="Charlie administration restricted"
        description="Requires the global charlie:manage permission. Read and approval permissions do not grant configuration access."
      />
    );
  const featureEnabled = flags.data?.["feature.charlie"] === true;
  const activeTabs: readonly CharlieAdminTab[] = featureEnabled
    ? CHARLIE_ADMIN_TABS
    : ["connection", "diagnostics"];
  const tab = activeTabs.includes(requestedTab) ? requestedTab : "connection";
  const select = (next: CharlieAdminTab) =>
    router.push(`/dashboard/settings/charlie?${mergeCharlieSearch(params, { tab: next })}`);
  const onTabKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const next = adjacentTab(activeTabs, tab, event.key);
    if (!next) return;
    event.preventDefault();
    select(next);
    document.getElementById(`charlie-admin-tab-${next}`)?.focus();
  };
  return (
    <div className="space-y-6">
      <div className="flex items-start gap-3">
        <Link
          href="/dashboard/settings"
          aria-label="Back to settings"
          className="mt-1 rounded p-1 text-muted-foreground hover:bg-accent"
        >
          <ArrowLeft className="h-4 w-4" />
        </Link>
        <div>
          <h1 className="text-2xl font-semibold">Charlie</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Connect and govern the external Charlie service and its Astronomer
            product agent.
          </p>
        </div>
      </div>
      {!featureEnabled && (
        <StatePanel
          icon={Sparkles}
          tone="warning"
          title="Charlie is disabled"
          description="Only locally stored connection metadata and network-quiesced diagnostics are available. Astronomer makes no product-agent or Charlie central request until an administrator explicitly enables the feature."
        />
      )}
      <div
        role="tablist"
        aria-label="Charlie administration"
        className="flex overflow-x-auto border-b border-border"
      >
        {activeTabs.map((value) => (
          <button
            key={value}
            id={`charlie-admin-tab-${value}`}
            type="button"
            role="tab"
            aria-selected={tab === value}
            aria-controls={`charlie-admin-panel-${value}`}
            tabIndex={tab === value ? 0 : -1}
            onKeyDown={onTabKey}
            onClick={() => select(value)}
            className={cn(
              "min-h-11 border-b-2 px-4 text-sm",
              tab === value
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {tabLabels[value]}
          </button>
        ))}
      </div>
      <div
        id={`charlie-admin-panel-${tab}`}
        role="tabpanel"
        tabIndex={0}
        aria-labelledby={`charlie-admin-tab-${tab}`}
      >
        {tab === "connection" && <ConnectionTab localOnly={!featureEnabled} />}
        {tab === "agent" && <AgentTab />}
        {tab === "mode" && <ModeTab />}
        {tab === "automation" && <AutomationTab />}
        {tab === "access" && <AccessTab />}
        {tab === "diagnostics" && <DiagnosticsTab />}
      </div>
    </div>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4 rounded-xl border border-border bg-card p-5">
      <div>
        <h2 className="font-semibold">{title}</h2>
        {description && (
          <p className="mt-1 text-xs text-muted-foreground">{description}</p>
        )}
      </div>
      {children}
    </section>
  );
}
function Unavailable({ name, retry }: { name: string; retry?: () => void }) {
  return (
    <StatePanel
      icon={AlertTriangle}
      tone="warning"
      title={`${name} unavailable`}
      description="The Astronomer gateway does not expose this Charlie capability yet, Charlie is unreachable, or access was denied. No configuration was changed."
      actionLabel={retry ? "Retry" : undefined}
      onAction={retry}
    />
  );
}
function Meta({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-all text-sm font-medium">{value || "—"}</dd>
    </div>
  );
}

const emptyOnboarding: CharlieOnboardingInput = {
  package: {},
  signingPublicKey: "",
  confirmedSigningKeyId: "",
  confirmedSigningFingerprint: "",
  expectedDeploymentId: "",
  expectedRouteId: "",
};
export function ConnectionTab({ localOnly = false }: { localOnly?: boolean } = {}) {
  const qc = useQueryClient();
  const connection = useQuery({
    queryKey: queryKeys.charlie.adminConnection,
    queryFn: getCharlieConnection,
    retry: false,
  });
  const [input, setInput] = useState(emptyOnboarding);
  const [fileName, setFileName] = useState("");
  const [fileError, setFileError] = useState("");
  const [validated, setValidated] = useState<CharlieOnboardingView>();
  const [confirm, setConfirm] = useState<"consume" | "disconnect" | null>(null);
  const [disclosure, setDisclosure] = useState(false);
  const validate = useMutation({
    mutationFn: validateCharlieOnboarding,
    onSuccess: (value) => {
      setValidated(value);
      toastSuccess("Charlie package signature validated locally");
    },
    onError: (e) => toastApiError("Package validation failed", e),
  });
  const consume = useMutation({
    mutationFn: consumeCharlieOnboarding,
    onSuccess: () => {
      setValidated(undefined);
      setInput(emptyOnboarding);
      setFileName("");
      setConfirm(null);
      setDisclosure(false);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminConnection,
      });
      toastSuccess("Charlie connection and product agent onboarding started");
    },
    onError: (e) => toastApiError("Charlie onboarding failed", e),
  });
  const disconnect = useMutation({
    mutationFn: disconnectCharlie,
    onSuccess: () => {
      setConfirm(null);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminConnection,
      });
      toastSuccess("Charlie disconnected");
    },
    onError: (e) => toastApiError("Disconnect failed", e),
  });
  const load = async (file: File | undefined) => {
    setFileError("");
    setValidated(undefined);
    if (!file) return;
    try {
      const raw = parseOnboardingFile(await file.text());
      const wrapper =
        raw.package &&
        typeof raw.package === "object" &&
        !Array.isArray(raw.package)
          ? raw
          : undefined;
      const string = (...keys: string[]) => {
        for (const k of keys) {
          const v = raw[k];
          if (typeof v === "string") return v;
        }
        return "";
      };
      setInput({
        package: (wrapper?.package as Record<string, unknown>) ?? raw,
        signingPublicKey: string("signing_public_key", "signingPublicKey"),
        confirmedSigningKeyId: string(
          "confirmed_signing_key_id",
          "confirmedSigningKeyId",
        ),
        confirmedSigningFingerprint: string(
          "confirmed_signing_fingerprint",
          "confirmedSigningFingerprint",
        ),
        expectedDeploymentId: string(
          "expected_deployment_id",
          "expectedDeploymentId",
        ),
        expectedRouteId: string("expected_route_id", "expectedRouteId"),
      });
      setFileName(file.name);
    } catch (e) {
      setFileName("");
      setInput(emptyOnboarding);
      setFileError(e instanceof Error ? e.message : "Invalid package");
    }
  };
  const complete =
    Object.keys(input.package).length > 0 &&
    input.signingPublicKey &&
    input.confirmedSigningKeyId &&
    /^[a-f0-9]{64}$/.test(input.confirmedSigningFingerprint) &&
    input.expectedDeploymentId &&
    input.expectedRouteId;
  const confirmValue = connection.data?.connected
    ? `REPLACE ${validated?.deploymentId ?? ""}`
    : `CONNECT ${validated?.deploymentId ?? ""}`;
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Section
        title="Current connection"
        description="Only non-secret identifiers and digests are displayed. Credentials and private keys are never returned."
      >
        {connection.isLoading ? (
          <Loader2 className="h-5 w-5 animate-spin motion-reduce:animate-none" />
        ) : connection.isError ? (
          <Unavailable
            name="Connection status"
            retry={() => void connection.refetch()}
          />
        ) : (
          <dl className="grid grid-cols-2 gap-4">
            <Meta
              label="Status"
              value={connection.data?.connected ? "Connected" : "Not connected"}
            />
            <Meta label="Product" value={connection.data?.productId} />
            <Meta label="Deployment" value={connection.data?.deploymentId} />
            <Meta label="Route" value={connection.data?.routeId} />
            <Meta
              label="Central version"
              value={connection.data?.centralVersion}
            />
            <Meta
              label="Signing key ID"
              value={connection.data?.signingKeyId}
            />
            <Meta
              label="Signing fingerprint"
              value={connection.data?.signingFingerprint}
            />
            <Meta
              label="Package digest"
              value={connection.data?.packageDigest}
            />
            <Meta
              label="Disclosure digest"
              value={connection.data?.disclosureDigest}
            />
            <Meta
              label="Disclosure acknowledged"
              value={connection.data?.disclosureAcknowledged ? "Yes" : "No"}
            />
          </dl>
        )}
        {connection.data?.connected && (
          <button
            onClick={() => setConfirm("disconnect")}
            className={`${button} mt-4 text-status-error`}
          >
            <Trash2 className="h-4 w-4" />
            Disconnect
          </button>
        )}
      </Section>
      {!localOnly && <Section
        title="Connect or replace Charlie"
        description="Upload a signed onboarding package. It is held only in memory and sent to local validation; package contents are never rendered or persisted by the browser."
      >
        <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          Model providers, LLM routing, RAG, knowledge sources, and agentic
          workflows remain administered in the separate Charlie service.
          Astronomer stores only the product trust, route, mode, policy, and
          product-agent integration needed to expose authorized capabilities.
        </p>
        <label className="flex cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed border-border p-5 text-sm hover:bg-accent">
          <Upload className="h-4 w-4" />
          <span>{fileName || "Choose JSON package"}</span>
          <input
            type="file"
            accept="application/json,.json"
            className="sr-only"
            aria-describedby="charlie-package-safety"
            onChange={(e) => void load(e.target.files?.[0])}
          />
        </label>
        <p
          id="charlie-package-safety"
          className="text-xs text-muted-foreground"
        >
          The selected file name is visible; credential values and package
          content are never added to the page.
        </p>
        {fileError && (
          <p role="alert" className="text-sm text-status-error">
            {fileError}
          </p>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            label="Signing public key"
            value={input.signingPublicKey}
            set={(v) => setInput((x) => ({ ...x, signingPublicKey: v }))}
          />
          <Field
            label="Signing key ID"
            value={input.confirmedSigningKeyId}
            set={(v) => setInput((x) => ({ ...x, confirmedSigningKeyId: v }))}
          />
          <Field
            label="Confirmed SHA-256 fingerprint"
            value={input.confirmedSigningFingerprint}
            set={(v) =>
              setInput((x) => ({ ...x, confirmedSigningFingerprint: v }))
            }
          />
          <Field
            label="Expected deployment ID"
            value={input.expectedDeploymentId}
            set={(v) => setInput((x) => ({ ...x, expectedDeploymentId: v }))}
          />
          <Field
            label="Expected route ID"
            value={input.expectedRouteId}
            set={(v) => setInput((x) => ({ ...x, expectedRouteId: v }))}
          />
        </div>
        <button
          disabled={!complete || validate.isPending}
          onClick={() => validate.mutate(input)}
          className={primary}
        >
          {validate.isPending ? (
            <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
          ) : (
            <Shield className="h-4 w-4" />
          )}
          Validate signature locally
        </button>
        {validated && (
          <div
            role="status"
            className="space-y-3 rounded-lg border border-status-success/30 bg-status-success/5 p-4"
          >
            <div className="flex items-center gap-2 text-sm font-medium">
              <CheckCircle2 className="h-4 w-4 text-status-success" />
              Validated package
            </div>
            <dl className="grid gap-3 sm:grid-cols-2">
              <Meta label="Package ID" value={validated.packageId} />
              <Meta label="Product" value={validated.productId} />
              <Meta label="Deployment" value={validated.deploymentId} />
              <Meta label="Logical agent" value={validated.logicalAgentId} />
              <Meta label="Route" value={validated.routeId} />
              <Meta
                label="Allowed routes"
                value={validated.allowedRouteIds.join(", ")}
              />
              <Meta label="Package schema" value={validated.schema} />
              <Meta
                label="Central API version"
                value={validated.centralApiVersion}
              />
              <Meta label="Replicas" value={validated.replicaCount} />
              <Meta label="Issued" value={validated.issuedAt} />
              <Meta label="Expires" value={validated.expiresAt} />
              <Meta label="Signing key ID" value={validated.signingKeyId} />
              <Meta
                label="Signing fingerprint"
                value={validated.signingFingerprint}
              />
              <Meta label="Package digest" value={validated.packageDigest} />
              <Meta
                label="Central trust fingerprint"
                value={validated.centralTrustFingerprint}
              />
              <Meta label="Agent image" value={validated.artifact.image} />
              <Meta
                label="Image manifest digest"
                value={validated.artifact.manifestDigest}
              />
              <Meta label="Agent chart" value={validated.artifact.chart} />
              <Meta
                label="Chart digest"
                value={validated.artifact.chartDigest}
              />
              <Meta label="Validation state" value={validated.state} />
            </dl>
            <label className="flex items-start gap-2 text-xs">
              <input
                type="checkbox"
                checked={disclosure}
                onChange={(e) => setDisclosure(e.target.checked)}
                className="mt-0.5"
              />
              <span>
                I reviewed the package source, expected deployment and route,
                signing fingerprint, and understand that changed capabilities or
                authority require a new disclosure acknowledgement.
              </span>
            </label>
            <button
              disabled={!disclosure}
              onClick={() => setConfirm("consume")}
              className={primary}
            >
              {connection.data?.connected
                ? "Replace connection"
                : "Connect Charlie"}
            </button>
          </div>
        )}
      </Section>}
      <ConfirmDialog
        open={confirm === "consume"}
        onClose={() => setConfirm(null)}
        onConfirm={() => consume.mutate(input)}
        title={
          connection.data?.connected
            ? "Replace Charlie connection"
            : "Connect Charlie"
        }
        description="This consumes the one-time package and installs or reconciles the separate Charlie product agent. Existing authority is never expanded by the browser."
        confirmText={
          connection.data?.connected ? "Replace connection" : "Connect"
        }
        confirmValue={confirmValue}
        loading={consume.isPending}
      />
      <ConfirmDialog
        open={confirm === "disconnect"}
        onClose={() => setConfirm(null)}
        onConfirm={() => disconnect.mutate()}
        title="Disconnect Charlie"
        description="This stops the Charlie integration while preserving Astronomer audit records."
        confirmText="Disconnect"
        confirmValue="DISCONNECT CHARLIE"
        variant="destructive"
        loading={disconnect.isPending}
      />
    </div>
  );
}
function Field({
  label,
  value,
  set,
  type = "text",
  min,
  max,
}: {
  label: string;
  value: string | number;
  set: (v: string) => void;
  type?: string;
  min?: number;
  max?: number;
}) {
  return (
    <label className="space-y-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <input
        type={type}
        min={min}
        max={max}
        value={value}
        onChange={(e) => set(e.target.value)}
        className={field}
      />
    </label>
  );
}

export function agentActionsForState(
  state: string,
): Array<"install" | "upgrade" | "rollback" | "rotate"> {
  if (["not_installed", "inactive", "disconnected"].includes(state))
    return ["install"];
  if (["ready", "degraded"].includes(state))
    return ["upgrade", "rollback", "rotate"];
  return [];
}

export function AgentTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAgent,
    queryFn: getCharlieAgent,
    retry: false,
    refetchInterval: 15000,
  });
  const connection = useQuery({
    queryKey: queryKeys.charlie.adminConnection,
    queryFn: getCharlieConnection,
    retry: false,
    refetchInterval: 15000,
  });
  const [confirm, setConfirm] = useState(false);
  const action = useMutation({
    mutationFn: (a: "install" | "upgrade" | "rollback" | "rotate") =>
      runCharlieAgentAction(a),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      toastSuccess("Charlie agent lifecycle request accepted");
    },
    onError: (e) => toastApiError("Agent action failed", e),
  });
  const uninstall = useMutation({
    mutationFn: uninstallCharlieAgent,
    onSuccess: () => {
      setConfirm(false);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      toastSuccess("Charlie agent uninstall requested");
    },
    onError: (e) => toastApiError("Uninstall failed", e),
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie agent"
      />
    );
  if (q.isError || !q.data)
    return <Unavailable name="Agent status" retry={() => void q.refetch()} />;
  const a = q.data;
  const trustReady = Boolean(
    connection.data?.connected &&
      connection.data.signingFingerprint &&
      connection.data.packageDigest,
  );
  const actions = agentActionsForState(a.applicationState);
  return (
    <Section
      title="Charlie product agent"
      description="Astronomer manages the product-side agent; Charlie central remains a separate service."
    >
      <div aria-live="polite">
        <StatusBadge
          status={a.applicationState}
          label={`Agent state: ${a.applicationState.replaceAll("_", " ")}`}
        />
      </div>
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Meta label="Argo application" value={a.applicationState} />
        <Meta
          label="Replicas"
          value={`${a.readyReplicas}/${a.desiredReplicas} ready`}
        />
        <Meta label="Leader" value={a.leaderReplica} />
        <Meta label="Standby" value={a.standbyReplicas?.join(", ")} />
        <Meta label="Fencing epoch" value={a.fencingEpoch} />
        <Meta
          label="Last heartbeat"
          value={
            a.lastHeartbeatAt
              ? formatRelativeTime(a.lastHeartbeatAt)
              : undefined
          }
        />
        <Meta label="Agent version" value={a.agentVersion} />
        <Meta label="Chart version" value={a.chartVersion} />
        <Meta label="Chart digest" value={a.chartDigest} />
        <Meta label="Image digest" value={a.imageDigest} />
      </dl>
      <div className="overflow-x-auto rounded-lg border border-border">
        <Table className="w-full min-w-[44rem] text-left text-sm">
          <caption className="sr-only">
            Product-observed Charlie agent replica status
          </caption>
          <TableHeader className="border-b border-border text-xs text-muted-foreground">
            <TableRow>
              <TableHead scope="col" className="p-3">Ordinal</TableHead>
              <TableHead scope="col" className="p-3">Instance</TableHead>
              <TableHead scope="col" className="p-3">Role</TableHead>
              <TableHead scope="col" className="p-3">State</TableHead>
              <TableHead scope="col" className="p-3">Last heartbeat</TableHead>
              <TableHead scope="col" className="p-3">Version</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className="divide-y divide-border">
            {a.replicas.length ? a.replicas.map((replica) => (
              <TableRow key={replica.ordinal}>
                <TableCell className="p-3">{replica.ordinal}</TableCell>
                <TableCell className="break-all p-3 font-mono text-xs">
                  {replica.instanceId || "Not reported"}
                </TableCell>
                <TableCell className="p-3">{replica.role}</TableCell>
                <TableCell className="p-3">
                  <StatusBadge status={replica.state} />
                </TableCell>
                <TableCell className="p-3">
                  {replica.lastHeartbeatAt
                    ? formatRelativeTime(replica.lastHeartbeatAt)
                    : "Not reported"}
                </TableCell>
                <TableCell className="p-3">{replica.version || "Not reported"}</TableCell>
              </TableRow>
            )) : (
              <TableRow>
                <TableCell colSpan={6} className="p-4 text-center text-muted-foreground">
                  No product-observed replica status is available.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <div className="flex flex-wrap gap-2">
        {actions.map((v) => (
          <button
            key={v}
            disabled={action.isPending || (v === "install" && !trustReady)}
            onClick={() => action.mutate(v)}
            className={button}
          >
            <RefreshCw className="h-4 w-4" />
            {v[0].toUpperCase() + v.slice(1)}
          </button>
        ))}
        <button
          disabled={action.isPending || uninstall.isPending || a.applicationState === "not_installed"}
          onClick={() => setConfirm(true)}
          className={`${button} text-status-error`}
        >
          <Trash2 className="h-4 w-4" />
          Uninstall
        </button>
      </div>
      {actions.includes("install") && !trustReady && (
        <p role="status" className="text-sm text-status-warning">
          Install is unavailable until a signed Charlie onboarding package has
          been validated and consumed into an active connection with recorded
          signing and package digests.
        </p>
      )}
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => uninstall.mutate()}
        title="Uninstall Charlie agent"
        description="This removes the Astronomer-side Charlie agent. It does not delete Charlie central data or Astronomer audit history."
        confirmText="Uninstall"
        confirmValue="UNINSTALL CHARLIE"
        variant="destructive"
        loading={uninstall.isPending}
      />
    </Section>
  );
}

const modeHelp: Record<CharlieMode, string> = {
  disabled:
    "No new sessions, triggers, findings, claims, approvals, actions, or MCP calls. Health and audit remain available.",
  read_only:
    "Charlie can investigate and explain through authorized reads, but cannot propose executable approvals.",
  approval:
    "Includes Read only. Charlie may propose bounded actions; an eligible authorized user must approve each exact action.",
  auto: "Includes Read only and Approval required. Charlie may additionally execute only capabilities explicitly allowed by current product policy and disclosure.",
};
const productModeLabel: Record<CharlieMode, string> = {
  disabled: "Disabled",
  read_only: "Read only",
  approval: "Approval required",
  auto: "Automation",
};
export function ModeTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminMode,
    queryFn: getCharlieMode,
    retry: false,
  });
  const [next, setNext] = useState<CharlieMode>();
  const [emergency, setEmergency] = useState(false);
  const change = useMutation({
    mutationFn: (mode: CharlieMode) =>
      updateCharlieMode(mode, q.data!.revision),
    onSuccess: (v) => {
      setNext(undefined);
      qc.setQueryData(queryKeys.charlie.adminMode, v);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess(
        `Charlie authoritative mode: ${productModeLabel[v.authoritative]}`,
      );
    },
    onError: (e) => toastApiError("Mode change was not confirmed", e),
  });
  const disable = useMutation({
    mutationFn: () => emergencyDisableCharlie(q.data!.revision),
    onSuccess: (v) => {
      setEmergency(false);
      qc.setQueryData(queryKeys.charlie.adminMode, v);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Emergency disable applied locally");
    },
    onError: (e) => toastApiError("Emergency disable failed", e),
  });
  const acknowledge = useMutation({
    mutationFn: (digest: string) => acknowledgeCharlieDisclosure(digest),
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminMode, v);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Disclosure digest acknowledged");
    },
    onError: (e) => toastApiError("Disclosure acknowledgement failed", e),
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie mode"
      />
    );
  if (q.isError || !q.data)
    return <Unavailable name="Mode control" retry={() => void q.refetch()} />;
  const m = q.data;
  const needsAck =
    !!m.disclosureDigest &&
    m.disclosureDigest !== m.acknowledgedDisclosureDigest;
  return (
    <div className="space-y-4">
      <Section
        title="Authority mode"
        description="Charlie is authoritative after readback. Drift can only reduce authority; the UI never assumes a requested mode took effect."
      >
        <div
          className="grid gap-3 md:grid-cols-2"
          role="group"
          aria-label="Charlie authority mode"
        >
          {(["disabled", "read_only", "approval", "auto"] as CharlieMode[]).map(
            (mode) => (
              <button
                key={mode}
                type="button"
                aria-pressed={m.authoritative === mode}
                disabled={
                  m.authoritative === mode ||
                  (needsAck && mode !== "disabled") ||
                  (mode === "auto" && m.autoReadiness?.ready === false) ||
                  change.isPending
                }
                onClick={() => setNext(mode)}
                className={cn(
                  "rounded-lg border p-4 text-left",
                  m.authoritative === mode
                    ? "border-primary bg-primary/5"
                    : "border-border hover:bg-accent",
                )}
              >
                <span className="font-medium">{productModeLabel[mode]}</span>
                <span className="mt-1 block text-xs text-muted-foreground">
                  {modeHelp[mode]}
                </span>
              </button>
            ),
          )}
        </div>
        {needsAck && (
          <p role="status" className="text-sm text-status-warning">
            Review and acknowledge the current disclosure before enabling a
            non-disabled authority mode.
          </p>
        )}
        <div className="rounded-lg border p-4">
          <div className="flex items-center justify-between gap-3">
            <div>
              <p className="text-sm font-medium">Automatic-action readiness</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Charlie can enter automation only when every product-side safety
                prerequisite is currently satisfied.
              </p>
            </div>
            <StatusBadge
              status={m.autoReadiness?.ready ? "healthy" : "degraded"}
              label={m.autoReadiness?.ready ? "Ready" : "Blocked"}
            />
          </div>
          {m.autoReadiness?.blockers?.length ? (
            <ul className="mt-3 space-y-2">
              {m.autoReadiness.blockers.map((blocker) => (
                <li key={blocker.code} className="rounded-md bg-muted p-3 text-xs">
                  <p className="font-medium">{blocker.message}</p>
                  <p className="mt-1 text-muted-foreground">
                    Next action: {blocker.nextAction}
                  </p>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-3 text-xs text-muted-foreground">
              {m.autoReadiness
                ? "No product-side readiness blockers are active."
                : "This Charlie version did not publish structured auto-readiness data."}
            </p>
          )}
        </div>
        <dl className="grid grid-cols-2 gap-4">
          <Meta label="Requested" value={productModeLabel[m.requested]} />
          <Meta
            label="Authoritative"
            value={productModeLabel[m.authoritative]}
          />
          <Meta label="Revision" value={m.revision} />
          <Meta
            label="Emergency disabled"
            value={m.emergencyDisabled ? "Yes" : "No"}
          />
          <Meta
            label="Disable propagation"
            value={m.disablePending ? "Pending agent confirmation" : "Confirmed"}
          />
        </dl>
        <div aria-live="polite" className="rounded-lg border p-4">
          <p className="text-sm font-medium">Authoritative effects</p>
          {m.effects.length ? (
            <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted-foreground">
              {m.effects.map((effect) => (
                <li key={effect}>{effect}</li>
              ))}
            </ul>
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">
              Charlie reported no additional effects.
            </p>
          )}
        </div>
        {needsAck && (
          <div className="rounded-lg border border-status-warning/40 bg-status-warning/5 p-4">
            <p className="text-sm font-medium">Disclosure changed</p>
            <p className="mt-1 break-all text-xs text-muted-foreground">
              Digest: {m.disclosureDigest}
            </p>
            <button
              onClick={() => acknowledge.mutate(m.disclosureDigest!)}
              className={`${button} mt-3`}
            >
              Acknowledge reviewed disclosure
            </button>
          </div>
        )}
      </Section>
      <Section
        title="Emergency control"
        description="Immediately fail closed for Charlie activity while preserving health, configuration, and audit access."
      >
        <button
          disabled={m.emergencyDisabled || m.disablePending || disable.isPending}
          onClick={() => setEmergency(true)}
          className={`${button} border-status-error text-status-error`}
        >
          <AlertTriangle className="h-4 w-4" />
          Emergency Disable
        </button>
      </Section>
      <ConfirmDialog
        open={!!next}
        onClose={() => setNext(undefined)}
        onConfirm={() => next && change.mutate(next)}
        title="Change Charlie mode"
        description={next ? modeHelp[next] : ""}
        confirmText="Change mode"
        confirmValue={
          next ? `CHANGE TO ${productModeLabel[next].toUpperCase()}` : undefined
        }
        loading={change.isPending}
      />
      <ConfirmDialog
        open={emergency}
        onClose={() => setEmergency(false)}
        onConfirm={() => disable.mutate()}
        title="Emergency Disable Charlie"
        description={modeHelp.disabled}
        confirmText="Disable Charlie"
        confirmValue="DISABLE CHARLIE"
        variant="destructive"
        loading={disable.isPending}
      />
    </div>
  );
}

const newAutomationRule = (): CharlieTriggerRule => ({
  id: "",
  name: "",
  enabled: false,
  sourceType: "event",
  severities: ["high", "critical"],
  scopes: [],
  cooldownSeconds: 1800,
  gracePeriodSeconds: 300,
  flapWindowSeconds: 900,
  flapCount: 3,
  fleetThresholdPercent: 25,
  minimumAgentVersion: "",
  suppressed: false,
  maximumAttempts: 3,
  deadLetterEnabled: true,
  serviceIdentity: "charlie-automation",
});

export function AutomationTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAutomation,
    queryFn: getCharlieAutomation,
    retry: false,
  });
  const deadLetters = useQuery({
    queryKey: queryKeys.charlie.adminTriggerEvents("dead"),
    queryFn: () => listCharlieTriggerEvents("dead", 0, 20),
    retry: false,
  });
  const [draft, setDraft] = useState<CharlieAutomationView>();
  const [deleteRule, setDeleteRule] = useState<CharlieTriggerRule>();
  const [retryEvent, setRetryEvent] = useState<CharlieTriggerEvent>();
  const [policyError, setPolicyError] = useState("");
  useEffect(() => {
    if (q.data) setDraft(structuredClone(q.data));
  }, [q.data]);
  const save = useMutation({
    mutationFn: updateCharlieAutomation,
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminAutomation, v);
      setDraft(structuredClone(v));
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Charlie automation configuration saved");
    },
    onError: (e) => toastApiError("Automation save failed", e),
  });
  const savePolicy = useMutation({
    mutationFn: updateCharlieActionPolicy,
    onMutate: () => setPolicyError(""),
    onSuccess: (policy) => {
      const replacePolicy = (value: CharlieAutomationView | undefined) =>
        value && {
          ...value,
          actionPolicies: value.actionPolicies.map((candidate) =>
            candidate.capability === policy.capability ? policy : candidate,
          ),
        };
      setDraft((value) => replacePolicy(value));
      qc.setQueryData<CharlieAutomationView>(
        queryKeys.charlie.adminAutomation,
        replacePolicy,
      );
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      toastSuccess(`Action policy saved: ${policy.capability}`);
    },
    onError: (error) => {
      const message =
        error instanceof Error
          ? error.message
          : "Astronomer could not confirm the action-policy update.";
      setPolicyError(message);
      toastApiError("Action-policy update failed", error);
    },
  });
  const remove = useMutation({
    mutationFn: deleteCharlieAutomationRule,
    onSuccess: () => {
      setDeleteRule(undefined);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAutomation });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Charlie automation rule deleted");
    },
    onError: (e) => toastApiError("Automation rule deletion failed", e),
  });
  const retry = useMutation({
    mutationFn: retryCharlieTriggerEvent,
    onSuccess: () => {
      setRetryEvent(undefined);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminTriggerEvents("dead"),
      });
      toastSuccess("Charlie trigger retry queued");
    },
    onError: (e) => toastApiError("Trigger retry failed", e),
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading automation"
      />
    );
  if (q.isError || !draft)
    return (
      <Unavailable
        name="Automation configuration"
        retry={() => void q.refetch()}
      />
    );
  const update = (
    index: number,
    patch: Partial<CharlieAutomationView["rules"][number]>,
  ) =>
    setDraft(
      (v) =>
        v && {
          ...v,
          rules: v.rules.map((r, i) => (i === index ? { ...r, ...patch } : r)),
        },
    );
  const updatePolicy = (
    index: number,
    patch: Partial<CharlieAutomationView["actionPolicies"][number]>,
  ) =>
    setDraft((value) =>
      value
        ? {
            ...value,
            actionPolicies: value.actionPolicies.map((policy, candidate) =>
              candidate === index ? { ...policy, ...patch } : policy,
            ),
          }
        : value,
    );
  const issues = automationValidationIssues(draft);
  return (
    <div className="space-y-4">
      <Section
        title="Automation identity"
        description="Astronomer evaluates product triggers and delegates only opaque, scoped authority. Charlie does not define this role engine."
      >
        <label className="flex gap-2 text-sm">
          <input
            type="checkbox"
            checked={draft.serviceIdentityEnabled}
            onChange={(e) =>
              setDraft({ ...draft, serviceIdentityEnabled: e.target.checked })
            }
          />
          Enable Charlie automation service identity
        </label>
        <p className="text-xs text-muted-foreground">
          Defaults revision {draft.defaultsRevision}
        </p>
      </Section>
      <Section
        title="Automatic-action policies"
        description="The exact intersection of Charlie's central capability allowlist and Astronomer's local budgets and safety controls. This view never includes action arguments or authority references."
      >
        {draft.actionPolicies.length ? (
          <div className="space-y-3">
            {draft.actionPolicies.map((policy, policyIndex) => {
              const mayEnable =
                policy.centralState === "verified" &&
                policy.centralAllowlisted &&
                policy.autoEligible;
              const original = q.data?.actionPolicies.find(
                (candidate) => candidate.capability === policy.capability,
              );
              const changed =
                !!original &&
                ([
                  "enabled",
                  "maxActionsPerIncident",
                  "maxActionsPerWindow",
                  "budgetWindowSeconds",
                  "cooldownSeconds",
                ] as const).some((field) => original[field] !== policy[field]);
              const valuesValid =
                policy.maxActionsPerIncident >= 1 &&
                policy.maxActionsPerIncident <= 100 &&
                policy.maxActionsPerWindow >= 1 &&
                policy.maxActionsPerWindow <= 100 &&
                policy.budgetWindowSeconds >= 60 &&
                policy.budgetWindowSeconds <= 86400 &&
                policy.cooldownSeconds >= 30 &&
                policy.cooldownSeconds <= 604800;
              return (
              <article key={policy.capability} className="rounded-lg border p-4">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <h3 className="font-mono text-sm font-medium">{policy.capability}</h3>
                    <p className="mt-1 text-xs text-muted-foreground">{policy.effect}</p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <StatusBadge status={policy.enabled ? "healthy" : "disabled"} label={policy.enabled ? "Enabled" : "Disabled"} />
                    <StatusBadge status={policy.centralState === "verified" ? "healthy" : "unavailable"} label={`Central: ${policy.centralState}`} />
                    <StatusBadge status={policy.circuitState} label={`Circuit: ${policy.circuitState}`} />
                  </div>
                </div>
                <dl className="mt-3 grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
                  <Meta label="Risk" value={policy.risk} />
                  <Meta label="Auto eligible" value={policy.autoEligible ? "Yes" : "No"} />
                  <Meta label="Central allowlisted" value={policy.centralAllowlisted ? "Yes" : "No"} />
                  <Meta label="Revision" value={policy.revision} />
                </dl>
                <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <NumberField label="Max actions per incident" value={policy.maxActionsPerIncident} min={1} max={100} set={(value) => updatePolicy(policyIndex, { maxActionsPerIncident: value })} />
                  <NumberField label="Max actions per window" value={policy.maxActionsPerWindow} min={1} max={100} set={(value) => updatePolicy(policyIndex, { maxActionsPerWindow: value })} />
                  <NumberField label="Budget window seconds" value={policy.budgetWindowSeconds} min={60} max={86400} set={(value) => updatePolicy(policyIndex, { budgetWindowSeconds: value })} />
                  <NumberField label="Cooldown seconds" value={policy.cooldownSeconds} min={30} max={604800} set={(value) => updatePolicy(policyIndex, { cooldownSeconds: value })} />
                </div>
                <label className="mt-3 flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    aria-label={`Enable ${policy.capability}`}
                    checked={policy.enabled}
                    disabled={!policy.enabled && !mayEnable}
                    onChange={(event) => updatePolicy(policyIndex, { enabled: event.target.checked })}
                  />
                  Enable bounded automatic action
                </label>
                {!mayEnable && !policy.enabled && (
                  <p className="mt-2 text-xs text-status-warning">
                    Enabling is unavailable until Charlie central is verified,
                    centrally allowlists this capability, and marks it auto eligible.
                  </p>
                )}
                <p className="mt-3 text-xs"><span className="font-medium">Scope:</span> {policy.scopeSummary}</p>
                <p className="mt-2 text-xs"><span className="font-medium">Verification:</span> {policy.verification}</p>
                <div className="mt-2 text-xs">
                  <span className="font-medium">Preconditions:</span>{" "}
                  {policy.preconditions.length ? policy.preconditions.join("; ") : "None published"}
                </div>
                <button
                  type="button"
                  className={`${primary} mt-3`}
                  disabled={!changed || !valuesValid || savePolicy.isPending}
                  onClick={() => savePolicy.mutate(policy)}
                >
                  <Save className="h-4 w-4" />
                  Save action policy
                </button>
              </article>
              );
            })}
            {policyError && (
              <p role="alert" className="rounded-lg border border-status-error/30 p-3 text-sm text-status-error">
                {policyError}
              </p>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No bounded automatic-action policy was published. Astronomer will
            not imply automatic authority.
          </p>
        )}
      </Section>
      {draft.rules.map((r, i) => (
        <Section
          key={r.id || `new-${i}`}
          title={r.name || "New trigger rule"}
          description={`${r.sourceType} · service identity ${r.serviceIdentity}`}
        >
          <label className="flex gap-2 text-sm">
            <input
              type="checkbox"
              checked={r.enabled}
              onChange={(e) => update(i, { enabled: e.target.checked })}
            />
            Enabled
          </label>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              label="Rule name"
              value={r.name}
              set={(v) => update(i, { name: v })}
            />
            <Field
              label="Source type"
              value={r.sourceType}
              set={(v) => update(i, { sourceType: v })}
            />
            <Field
              label="Severities (comma separated)"
              value={r.severities.join(", ")}
              set={(v) =>
                update(i, {
                  severities: v
                    .split(",")
                    .map((value) => value.trim().toLowerCase())
                    .filter(Boolean),
                })
              }
            />
            <Field
              label="Service identity"
              value={r.serviceIdentity}
              set={(v) => update(i, { serviceIdentity: v })}
            />
            <NumberField
              label="Cooldown seconds"
              value={r.cooldownSeconds}
              min={1}
              set={(v) => update(i, { cooldownSeconds: v })}
            />
            <NumberField
              label="Grace period seconds"
              value={r.gracePeriodSeconds}
              min={1}
              set={(v) => update(i, { gracePeriodSeconds: v })}
            />
            <NumberField
              label="Flap window seconds"
              value={r.flapWindowSeconds}
              min={1}
              set={(v) => update(i, { flapWindowSeconds: v })}
            />
            <NumberField
              label="Flap count"
              value={r.flapCount}
              min={1}
              set={(v) => update(i, { flapCount: v })}
            />
            <NumberField
              label="Fleet threshold %"
              value={r.fleetThresholdPercent}
              min={0}
              max={100}
              set={(v) => update(i, { fleetThresholdPercent: v })}
            />
            <NumberField
              label="Maximum attempts"
              value={r.maximumAttempts}
              min={1}
              set={(v) => update(i, { maximumAttempts: v })}
            />
            <Field
              label="Minimum agent version"
              value={r.minimumAgentVersion ?? ""}
              set={(v) => update(i, { minimumAgentVersion: v })}
            />
            <Field
              label="Scopes (comma separated)"
              value={r.scopes.join(", ")}
              set={(v) =>
                update(i, {
                  scopes: v
                    .split(",")
                    .map((x) => x.trim())
                    .filter(Boolean),
                })
              }
            />
          </div>
          <div className="flex flex-wrap gap-4">
            <label className="flex gap-2 text-sm">
              <input
                type="checkbox"
                checked={r.suppressed}
                onChange={(e) => update(i, { suppressed: e.target.checked })}
              />
              Suppressed
            </label>
            <label className="flex gap-2 text-sm">
              <input
                type="checkbox"
                checked={r.deadLetterEnabled}
                onChange={(e) =>
                  update(i, { deadLetterEnabled: e.target.checked })
                }
              />
              Dead letters
            </label>
            <button
              type="button"
              className={`${button} text-status-error`}
              onClick={() => {
                if (r.id) setDeleteRule(r);
                else
                  setDraft((value) =>
                    value && {
                      ...value,
                      rules: value.rules.filter((_, index) => index !== i),
                    },
                  );
              }}
            >
              <Trash2 className="h-4 w-4" />
              Delete rule
            </button>
          </div>
        </Section>
      ))}
      {issues.length > 0 && (
        <div role="alert" className="rounded-lg border border-status-error/30 p-4">
          <p className="text-sm font-medium">Automation needs attention</p>
          <ul className="mt-2 list-disc pl-5 text-xs text-muted-foreground">
            {issues.map((issue) => (
              <li key={issue}>{issue}</li>
            ))}
          </ul>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() =>
            setDraft({ ...draft, rules: [...draft.rules, newAutomationRule()] })
          }
          className={button}
        >
          <Plus className="h-4 w-4" />
          Add trigger rule
        </button>
        <button
          type="button"
          disabled={save.isPending || issues.length > 0}
          onClick={() => save.mutate(draft)}
          className={primary}
        >
          <Save className="h-4 w-4" />
          Save automation
        </button>
      </div>
      <Section
        title="Dead-letter events"
        description="Bounded lifecycle metadata for failed trigger work. Charlie task content, fingerprints, sessions, and origin details are never exposed here."
      >
        {deadLetters.isLoading ? (
          <p role="status" className="text-sm text-muted-foreground">
            Loading dead-letter events…
          </p>
        ) : deadLetters.isError ? (
          <Unavailable
            name="Dead-letter events"
            retry={() => void deadLetters.refetch()}
          />
        ) : deadLetters.data?.length ? (
          <div className="overflow-x-auto">
            <Table className="w-full min-w-[760px] text-left text-sm">
              <caption className="sr-only">
                Charlie dead-letter trigger lifecycle metadata
              </caption>
              <TableHeader className="text-xs text-muted-foreground">
                <TableRow>
                  <TableHead scope="col" className="px-2 py-2">Event</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Resource</TableHead>
                  <TableHead scope="col" className="px-2 py-2">State</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Attempts</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Last error</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Dead-lettered</TableHead>
                  <TableHead scope="col" className="px-2 py-2"><span className="sr-only">Actions</span></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-border">
                {deadLetters.data.map((event) => (
                  <TableRow key={event.id}>
                    <TableCell className="px-2 py-2">
                      <span className="block font-medium">{event.eventType}</span>
                      <span className="block max-w-48 truncate font-mono text-xs text-muted-foreground" title={event.id}>
                        {event.id}
                      </span>
                    </TableCell>
                    <TableCell className="px-2 py-2">
                      {event.resourceType} · {event.resourceId}
                    </TableCell>
                    <TableCell className="px-2 py-2"><StatusBadge status={event.state} /></TableCell>
                    <TableCell className="px-2 py-2">{event.attemptCount}</TableCell>
                    <TableCell className="px-2 py-2">{event.lastErrorCode || "—"}</TableCell>
                    <TableCell className="px-2 py-2">
                      {event.deadLetteredAt
                        ? formatRelativeTime(event.deadLetteredAt)
                        : "—"}
                    </TableCell>
                    <TableCell className="px-2 py-2 text-right">
                      <button
                        type="button"
                        className={button}
                        onClick={() => setRetryEvent(event)}
                      >
                        <RefreshCw className="h-4 w-4" />
                        Retry
                      </button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No dead-letter trigger events.
          </p>
        )}
      </Section>
      <ConfirmDialog
        open={!!deleteRule}
        onClose={() => setDeleteRule(undefined)}
        onConfirm={() => deleteRule && remove.mutate(deleteRule.id)}
        title="Delete Charlie trigger rule"
        description={`Delete ${deleteRule?.name || "this trigger rule"}. Existing audit and dead-letter records are preserved.`}
        confirmText="Delete rule"
        confirmValue="DELETE TRIGGER"
        variant="destructive"
        loading={remove.isPending}
      />
      <ConfirmDialog
        open={!!retryEvent}
        onClose={() => setRetryEvent(undefined)}
        onConfirm={() => retryEvent && retry.mutate(retryEvent.id)}
        title="Retry Charlie trigger event"
        description={`Create one new retry attempt for ${retryEvent?.eventType || "this event"}. The dead source event remains immutable.`}
        confirmText="Queue retry"
        confirmValue="RETRY TRIGGER"
        loading={retry.isPending}
      />
    </div>
  );
}
function NumberField({
  label,
  value,
  set,
  min = 0,
  max,
}: {
  label: string;
  value: number;
  set: (v: number) => void;
  min?: number;
  max?: number;
}) {
  return (
    <Field
      label={label}
      type="number"
      value={value}
      min={min}
      max={max}
      set={(v) =>
        set(
          Math.min(
            max ?? Number.MAX_SAFE_INTEGER,
            Math.max(min, Number(v) || min),
          ),
        )
      }
    />
  );
}

export function AccessTab() {
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAccess,
    queryFn: getCharlieAccess,
    retry: false,
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie access"
      />
    );
  if (q.isError)
    return <Unavailable name="Access report" retry={() => void q.refetch()} />;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <GrantList
        title="Effective user permissions"
        items={q.data?.effectivePermissions ?? []}
      />
      <GrantList
        title="Automation grants"
        items={q.data?.automationGrants ?? []}
      />
    </div>
  );
}
function GrantList({
  title,
  items,
}: {
  title: string;
  items: Array<{ permission: string; scope: string; source: string }>;
}) {
  return (
    <Section
      title={title}
      description="Derived from Astronomer RBAC; this is not a parallel Charlie role system."
    >
      {items.length ? (
        items.map((g, i) => (
          <div
            key={`${g.permission}:${g.scope}:${i}`}
            className="flex items-start gap-3 rounded border p-3"
          >
            <KeyRound className="mt-0.5 h-4 w-4 text-muted-foreground" />
            <div>
              <p className="text-sm font-medium">{g.permission}</p>
              <p className="text-xs text-muted-foreground">
                {g.scope} · {g.source}
              </p>
            </div>
          </div>
        ))
      ) : (
        <p className="text-sm text-muted-foreground">No effective grants.</p>
      )}
    </Section>
  );
}

const diagnosticIcon: Record<string, typeof Database> = {
  local_config: Database,
  product_bridge_mtls: Network,
  agent_primary: Bot,
  agent_standby: Bot,
  central_via_agent: Network,
  leader_epoch: Activity,
  route_rag: Sparkles,
  mcp_tls_discovery: Shield,
  oci_artifacts: Database,
  credential_expiry: KeyRound,
};
export function DiagnosticsTab() {
  const q = useQuery({
    queryKey: queryKeys.charlie.adminDiagnostics,
    queryFn: getCharlieDiagnostics,
    retry: false,
    refetchInterval: 30000,
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Running Charlie diagnostics"
      />
    );
  if (q.isError)
    return <Unavailable name="Diagnostics" retry={() => void q.refetch()} />;
  const checks = completeDiagnostics(q.data?.checks ?? []);
  return (
    <Section
      title="Independent diagnostic checks"
      description="Charlie health never participates in Astronomer's core readiness. Failures degrade only Charlie features."
    >
      <div className="flex items-center justify-between">
        <StatusBadge
          status={q.data?.overall ?? "unknown"}
          label={`Overall: ${q.data?.overall ?? "unknown"}`}
        />
        <button onClick={() => void q.refetch()} className={button}>
          <RefreshCw className="h-4 w-4" />
          Run again
        </button>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        {checks.map((check) => {
          const Icon = diagnosticIcon[check.id] ?? Activity;
          return (
            <article
              key={check.id}
              className="rounded-lg border border-border p-4"
            >
              <div className="flex items-center gap-2">
                <Icon className="h-4 w-4" />
                <h3 className="flex-1 text-sm font-medium">{check.label}</h3>
                <StatusBadge status={check.state} />
              </div>
              <p className="mt-2 text-xs text-muted-foreground">
                {check.summary}
              </p>
              {check.expiresAt && (
                <p className="mt-1 text-xs text-muted-foreground">
                  Expires {formatRelativeTime(check.expiresAt)}
                </p>
              )}
              {check.nextAction && (
                <p className="mt-2 rounded-md bg-muted p-2 text-xs">
                  Next action: {check.nextAction}
                </p>
              )}
            </article>
          );
        })}
      </div>
      {q.data?.correlationId && (
        <p className="text-xs text-muted-foreground">
          Correlation: <span className="font-mono">{q.data.correlationId}</span>
        </p>
      )}
    </Section>
  );
}
