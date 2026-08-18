import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { User } from "@/types";

const api = vi.hoisted(() => ({
  acknowledgeCharlieDisclosure: vi.fn(),
  consumeCharlieOnboarding: vi.fn(),
  consumeCharlieConnect: vi.fn(),
  validateCharlieConnect: vi.fn(),
  deleteCharlieAutomationRule: vi.fn(),
  disconnectCharlie: vi.fn(),
  emergencyDisableCharlie: vi.fn(),
  getCharlieAccess: vi.fn(),
  getCharlieActivation: vi.fn(),
  getCharlieAgent: vi.fn(),
  getCharlieAlertPolicy: vi.fn(),
  getCharlieAutomation: vi.fn(),
  getCharlieConnection: vi.fn(),
  getCharlieDiagnostics: vi.fn(),
  getCharlieKubernetesVisibility: vi.fn(),
  getCharlieMode: vi.fn(),
  listCharlieTriggerEvents: vi.fn(),
  retryCharlieTriggerEvent: vi.fn(),
  updateCharlieAutomation: vi.fn(),
  updateCharlieActionPolicy: vi.fn(),
  updateCharlieAlertPolicy: vi.fn(),
  updateCharlieMode: vi.fn(),
  updateCharlieKubernetesVisibility: vi.fn(),
  validateCharlieOnboarding: vi.fn(),
}));
const navigation = vi.hoisted(() => ({
  params: new URLSearchParams("tab=connection&context=cluster-a"),
  push: vi.fn(),
}));
const feature = vi.hoisted(() => ({
  value: {
    data: { "feature.charlie": true } as
      | { "feature.charlie": boolean }
      | undefined,
    isError: false,
    refetch: vi.fn(),
  },
}));
const auth = vi.hoisted(() => ({
  user: { id: "admin", isSuperuser: true } as User | null,
}));

vi.mock("@tanstack/react-router", () => ({
  createFileRoute: () => (config: unknown) => config,
  lazyRouteComponent: () => () => null,
}));
vi.mock("@/lib/hooks", () => ({ useFeatureFlags: () => feature.value }));
vi.mock("@/lib/store", () => ({
  useAuthStore: (selector: (state: { user: User | null }) => unknown) =>
    selector({ user: auth.user }),
}));
vi.mock("@/lib/navigation", () => ({
  useRouter: () => ({ push: navigation.push }),
  useSearchParams: () => navigation.params,
}));
vi.mock("@/lib/link", () => ({
  Link: ({ href, children, ...props }: { href: string; children: ReactNode }) => (
    <a href={href} {...props}>{children}</a>
  ),
}));
vi.mock("@/lib/toast", () => ({ toastApiError: vi.fn(), toastSuccess: vi.fn() }));
vi.mock("@/lib/api/charlie-admin", () => api);

import {
  AccessTab,
  AgentTab,
  AlertsTab,
  AutomationTab,
  CharlieAdminContent,
  ConnectionTab,
  DiagnosticsTab,
  KubernetesTab,
  ModeTab,
} from "@/routes/dashboard/settings/charlie";

const CharlieAdminPage = CharlieAdminContent;

const digest = (letter: string) => `sha256:${letter.repeat(64)}`;
const safeReview = {
  packageId: "onboard_11111111111141118111111111111111",
  productId: "product-astronomer",
  productSlug: "astronomer" as const,
  deploymentId: "deployment-a",
  logicalAgentId: "agent-a",
  routeId: "route-a",
  allowedRouteIds: ["route-a"],
  schema: "charlie.onboarding/v1" as const,
  centralApiVersion: "charlie/v1" as const,
  centralTrustFingerprint: "c".repeat(64),
  signingKeyId: "key-a",
  signingFingerprint: "f".repeat(64),
  packageDigest: "p".repeat(64),
  artifact: {
    image: `registry.example/charlie/agent@${digest("a")}`,
    manifestDigest: digest("a"),
    chart: "oci://registry.example/charlie/agent",
    chartDigest: digest("b"),
  },
  replicaCount: 2,
  issuedAt: "2026-08-05T10:00:00Z",
  expiresAt: "2026-08-05T11:00:00Z",
  state: "validated" as const,
  idempotent: false,
};

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
function renderWithClient(node: ReactNode) {
  return render(
    <QueryClientProvider client={client()}>{node}</QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  navigation.params = new URLSearchParams("tab=connection&context=cluster-a");
  feature.value = { data: { "feature.charlie": true }, isError: false, refetch: vi.fn() };
  auth.user = { id: "admin", isSuperuser: true } as User;
  api.getCharlieActivation.mockResolvedValue({
    activated: true,
    endpoint: "https://charlie.example.test",
  });
  api.getCharlieConnection.mockResolvedValue({
    connected: true,
    productId: "astronomer",
    deploymentId: "deployment-a",
    routeId: "route-a",
    centralVersion: "1.0.0",
    signingKeyId: "key-a",
    signingFingerprint: "f".repeat(64),
    packageDigest: "p".repeat(64),
    disclosureDigest: digest("d"),
    disclosureAcknowledged: true,
  });
  api.getCharlieAgent.mockResolvedValue({
    applicationState: "ready",
    desiredReplicas: 2,
    readyReplicas: 2,
    leaderReplica: "instance-0",
    standbyReplicas: ["instance-1"],
    replicas: [
      { ordinal: 0, instanceId: "instance-0", role: "leader", state: "ready", lastHeartbeatAt: "2026-08-05T10:00:00Z", version: "1.0.0" },
      { ordinal: 1, instanceId: "instance-1", role: "standby", state: "ready", lastHeartbeatAt: "2026-08-05T09:59:59Z", version: "1.0.0" },
    ],
    fencingEpoch: 9,
    lastHeartbeatAt: "2026-08-05T10:00:00Z",
    agentVersion: "1.0.0",
    chartVersion: "1.0.0",
    chartDigest: digest("b"),
    imageDigest: digest("a"),
  });
  api.getCharlieAlertPolicy.mockResolvedValue({
    enabled: true,
    minimumSeverity: "medium",
    dedupeWindowSeconds: 1800,
    escalationAfterSeconds: 3600,
    quietHoursEnabled: false,
    quietHoursStart: "22:00",
    quietHoursEnd: "07:00",
    quietHoursTimezone: "UTC",
    revision: 1,
    channelIds: ["channel-a"],
    channels: [{ id: "channel-a", name: "Platform on-call", type: "slack", enabled: true, destinationConfigured: true }],
    availableChannels: [{ id: "channel-a", name: "Platform on-call", type: "slack", enabled: true, destinationConfigured: true }],
    inAppEnabled: true,
  });
  api.getCharlieMode.mockResolvedValue({
    requested: "approval",
    authoritative: "approval",
    revision: 4,
    emergencyDisabled: false,
    disablePending: false,
    disclosureDigest: digest("d"),
    acknowledgedDisclosureDigest: digest("c"),
    effects: ["Every write requires exact approval"],
    workloadCeiling: "approval",
    workloadCeilingReady: true,
  });
  api.getCharlieAutomation.mockResolvedValue({
    defaultsRevision: 3,
    serviceIdentityEnabled: true,
    actionPolicies: [{
      capability: "astronomer.queue.retry_task", effect: "retry one failed task", risk: "low",
      autoEligible: true, centralAllowlisted: true, centralState: "verified", enabled: true, revision: 2,
      maxActionsPerIncident: 1, maxActionsPerWindow: 3, budgetWindowSeconds: 3600,
      cooldownSeconds: 60, scopeSummary: "exact session resource ID",
      preconditions: ["task is retryable"], verification: "task reaches a terminal state",
      circuitState: "closed",
    }],
    rules: [{
      id: "rule-a", name: "Agent flap", enabled: true, sourceType: "agent",
      severities: ["high"], scopes: ["production"], cooldownSeconds: 1800,
      gracePeriodSeconds: 300, flapWindowSeconds: 900, flapCount: 3,
      fleetThresholdPercent: 25, minimumAgentVersion: "1.0.0", suppressed: false,
      maximumAttempts: 3, deadLetterEnabled: true, serviceIdentity: "system:charlie-automation", modeCeiling: "read_only",
    }],
  });
  api.getCharlieAccess.mockResolvedValue({ effectivePermissions: [], automationGrants: [] });
  api.listCharlieTriggerEvents.mockResolvedValue([]);
  api.getCharlieDiagnostics.mockResolvedValue({ overall: "healthy", checks: [], correlationId: "correlation-a" });
  api.getCharlieKubernetesVisibility.mockResolvedValue({
    schema: "charlie.kubernetes-visibility/v1",
    profile: "cluster_diagnostics",
    revision: 4,
    state: "enabled",
    instanceId: "astronomer-management-plane",
    namespaces: ["astronomer"],
    productOwnedOnly: true,
    clusterScoped: true,
    podLogs: false,
    downstreamTargets: false,
    secretValues: false,
    exec: false,
    attach: false,
    portForward: false,
    apiProxy: false,
    requiresRediscovery: false,
    requiresCentralReview: false,
    requiresProductAcknowledgement: false,
    availableProfiles: ["disabled", "product_namespace", "cluster_diagnostics"],
    scopeSummary: "Product-owned management resources plus bounded cluster diagnostics; downstream clusters excluded",
  });
  api.disconnectCharlie.mockResolvedValue({ connected: false });
  api.validateCharlieOnboarding.mockResolvedValue(safeReview);
  api.updateCharlieActionPolicy.mockImplementation(async (policy) => ({
    capability: policy.capability,
    effect: "retry one failed task",
    risk: "low",
    autoEligible: true,
    centralAllowlisted: true,
    centralState: "verified",
    enabled: policy.enabled,
    revision: 3,
    maxActionsPerIncident: policy.maxActionsPerIncident,
    maxActionsPerWindow: policy.maxActionsPerWindow,
    budgetWindowSeconds: policy.budgetWindowSeconds,
    cooldownSeconds: policy.cooldownSeconds,
    scopeSummary: "exact session resource ID",
    preconditions: ["task is retryable"],
    verification: "task reaches a terminal state",
    circuitState: "closed",
  }));
  api.updateCharlieAlertPolicy.mockImplementation(async (policy) => ({ ...policy, revision: policy.revision + 1 }));
});

afterEach(cleanup);

describe("Charlie administration acceptance", () => {
  it("configures Kubernetes observation independently while keeping hard prohibitions visible", async () => {
    api.updateCharlieKubernetesVisibility.mockResolvedValue({
      ...(await api.getCharlieKubernetesVisibility()),
      profile: "product_namespace",
      clusterScoped: false,
      requiresRediscovery: false,
      requiresCentralReview: true,
      candidateDisclosureDigest: "d".repeat(64),
    });
    renderWithClient(<KubernetesTab />);
    expect(await screen.findByText("Kubernetes API visibility")).toBeInTheDocument();
    expect(screen.getByText(/Downstream clusters, Secret values, exec, attach/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: "Product namespace" }));
    fireEvent.click(screen.getByRole("button", { name: "Save visibility policy" }));
    await waitFor(() => expect(api.updateCharlieKubernetesVisibility).toHaveBeenCalledWith({
      profile: "product_namespace", podLogs: false, revision: 4,
    }));
  });

  it("configures product-owned alert routing without implying action authority", async () => {
    renderWithClient(<AlertsTab />);
    expect(await screen.findByText("Actionable finding alerts")).toBeInTheDocument();
    expect(screen.getByText(/cannot approve, authorize, or dispatch work/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox", { name: "Route Charlie alerts to Platform on-call" }));
    fireEvent.click(screen.getByRole("button", { name: "Save alert policy" }));
    await waitFor(() => expect(api.updateCharlieAlertPolicy).toHaveBeenCalledTimes(1));
    expect(api.updateCharlieAlertPolicy).toHaveBeenCalledWith(
      expect.objectContaining({ channelIds: [], minimumSeverity: "medium" }),
      expect.anything(),
    );
  });

  it("separates operator Charlie grants from the automation identity", async () => {
    api.getCharlieAccess.mockResolvedValue({
      effectivePermissions: [
        { permission: "charlie:manage", scope: "global", source: "superuser" },
      ],
      automationGrants: [
        { permission: "charlie:read", scope: "global", source: "global_role:Charlie Automation" },
      ],
    });
    renderWithClient(<AccessTab />);
    expect(await screen.findByText("Your Charlie permissions")).toBeInTheDocument();
    expect(screen.getByText("Automation service identity")).toBeInTheDocument();
    expect(screen.getByText("charlie:manage")).toBeInTheDocument();
    expect(screen.getByText(/superuser/)).toBeInTheDocument();
    expect(screen.getByText("charlie:read")).toBeInTheDocument();
    expect(screen.queryByText("Effective user permissions")).toBeNull();
  });

  it("covers disabled, loading, denied, and authorized feature/permission states", async () => {
    feature.value = { data: { "feature.charlie": false }, isError: false, refetch: vi.fn() };
    const disabled = renderWithClient(<CharlieAdminPage />);
    expect(screen.getByText("Charlie is disabled")).toBeInTheDocument();
    expect(await screen.findByText("Charlie is connected")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Connection" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Diagnostics" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Agent" })).toBeNull();
    expect(screen.queryByText("Connect Charlie")).toBeNull();
    expect(api.getCharlieAgent).not.toHaveBeenCalled();
    expect(api.getCharlieMode).not.toHaveBeenCalled();
    expect(api.getCharlieAutomation).not.toHaveBeenCalled();
    expect(api.getCharlieAccess).not.toHaveBeenCalled();
    disabled.unmount();

    navigation.params = new URLSearchParams("tab=diagnostics");
    const diagnostics = renderWithClient(<CharlieAdminPage />);
    expect(await screen.findByText("Independent diagnostic checks")).toBeInTheDocument();
    expect(api.getCharlieDiagnostics).toHaveBeenCalledTimes(1);
    diagnostics.unmount();

    feature.value = { data: undefined, isError: false, refetch: vi.fn() };
    const loading = renderWithClient(<CharlieAdminPage />);
    expect(screen.getByText("Loading Charlie settings")).toBeInTheDocument();
    loading.unmount();

    feature.value = { data: { "feature.charlie": true }, isError: false, refetch: vi.fn() };
    auth.user = { id: "reader" } as User;
    const denied = renderWithClient(<CharlieAdminPage />);
    expect(screen.getByText("Charlie administration restricted")).toBeInTheDocument();
    denied.unmount();

    auth.user = { id: "admin", isSuperuser: true } as User;
    navigation.params = new URLSearchParams("tab=connection&context=cluster-a");
    renderWithClient(<CharlieAdminPage />);
    expect(await screen.findByRole("tab", { name: "Connection" })).toHaveAttribute("tabindex", "0");
  });

  it("uses roving keyboard tabs while preserving unrelated deep-link context", async () => {
    renderWithClient(<CharlieAdminPage />);
    const connection = await screen.findByRole("tab", { name: "Connection" });
    fireEvent.keyDown(connection, { key: "ArrowRight" });
    expect(navigation.push).toHaveBeenCalledWith(
      "/dashboard/settings/charlie?tab=agent&context=cluster-a",
    );
    expect(screen.getByRole("tab", { name: "Agent" })).toHaveAttribute("tabindex", "-1");
  });

  it("requires a typed disconnect confirmation before deactivating Charlie", async () => {
    renderWithClient(<ConnectionTab />);
    fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    expect(screen.getByText("Disconnect Charlie")).toBeInTheDocument();
    expect(
      screen.getByText(/Charlie navigation, findings, sessions, and the product agent stop/i),
    ).toBeInTheDocument();
    const dialogConfirm = screen.getAllByRole("button", { name: "Disconnect" }).at(-1);
    expect(dialogConfirm).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("DISCONNECT CHARLIE"), {
      target: { value: "DISABLE CHARLIE" },
    });
    expect(dialogConfirm).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("DISCONNECT CHARLIE"), {
      target: { value: "DISCONNECT CHARLIE" },
    });
    expect(dialogConfirm).toBeEnabled();
    fireEvent.click(dialogConfirm!);
    await waitFor(() => expect(api.disconnectCharlie).toHaveBeenCalledTimes(1));
  });

  it("hides disconnect when Charlie is not connected", async () => {
    api.getCharlieActivation.mockResolvedValue({ activated: false });
    api.getCharlieConnection.mockResolvedValue({ connected: false });
    renderWithClient(<ConnectionTab />);
    expect(await screen.findByText("Connect Charlie")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
  });

  it("hides connect until the current Charlie connection is disconnected", async () => {
    renderWithClient(<ConnectionTab />);
    expect(await screen.findByText("Charlie is connected")).toBeInTheDocument();
    expect(screen.getByText("https://charlie.example.test")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Disconnect" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Connect token")).toBeNull();
    expect(screen.queryByRole("button", { name: "Validate connection" })).toBeNull();
  });

  it("connects Charlie with endpoint and connect token", async () => {
    api.getCharlieActivation.mockResolvedValue({ activated: false });
    api.validateCharlieConnect.mockResolvedValue(safeReview);
    api.consumeCharlieConnect.mockResolvedValue({ ...safeReview, state: "consumed" });
    renderWithClient(<ConnectionTab />);
    fireEvent.change(await screen.findByLabelText("Charlie endpoint"), {
      target: { value: "https://charlie.example.test" },
    });
    fireEvent.change(screen.getByLabelText("Connect token"), {
      target: { value: "charlie.connect.v1.abc" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Validate connection" }));
    await waitFor(() =>
      expect(api.validateCharlieConnect).toHaveBeenCalledWith({
        endpoint: "https://charlie.example.test",
        connectToken: "charlie.connect.v1.abc",
      }),
    );
    expect(await screen.findByText("Validated package")).toBeInTheDocument();
  });

  it("renders only verified safe onboarding metadata and never package credentials", async () => {
    api.getCharlieActivation.mockResolvedValue({ activated: false });
    const secret = "enrollment-secret-dom-canary";
    const artifactCredential = "artifact-secret-dom-canary";
    const signature = "signed-package-dom-canary";
    const certificatePrivateKey = "certificate-private-key-dom-canary";
    const runtimeToken = "runtime-token-dom-canary";
    const view = renderWithClient(<ConnectionTab />);
    expect(await screen.findByText("Connect Charlie")).toBeInTheDocument();
    const file = {
      name: "charlie-onboarding.json",
      text: async () => JSON.stringify({
        package: {
          credentials: [{ credential: secret }, { credential: artifactCredential }],
          certificate_private_key: certificatePrivateKey,
          runtime_token: runtimeToken,
          signature,
        },
        signing_public_key: "public-key",
        confirmed_signing_key_id: "key-a",
        confirmed_signing_fingerprint: "f".repeat(64),
        expected_deployment_id: "deployment-a",
        expected_route_id: "route-a",
      }),
    };
    fireEvent.change(view.container.querySelector('input[type="file"]')!, {
      target: { files: [file] },
    });
    const validate = await screen.findByRole("button", { name: "Validate signature locally" });
    await waitFor(() => expect(validate).toBeEnabled());
    fireEvent.click(validate);
    await waitFor(() => expect(api.validateCharlieOnboarding).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Validated package")).toBeInTheDocument();
    expect(screen.getByText("charlie/v1")).toBeInTheDocument();
    expect(screen.getAllByText(safeReview.signingFingerprint).length).toBeGreaterThan(0);
    for (const canary of [
      secret,
      artifactCredential,
      certificatePrivateKey,
      runtimeToken,
      signature,
    ]) {
      expect(view.container.textContent).not.toContain(canary);
      expect(view.container.innerHTML).not.toContain(canary);
    }
  });

  it("shows truthful per-replica state and routes lifecycle changes through Flux delivery", async () => {
    renderWithClient(<AgentTab />);
    expect((await screen.findAllByText("instance-0")).length).toBeGreaterThan(0);
    expect(screen.getAllByText("instance-1").length).toBeGreaterThan(0);
    expect(screen.getByText(/installed from Charlie when you connect/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /install|upgrade|rollback|rotate|uninstall/i })).toBeNull();
  });

  it("keeps missing-agent state read-only instead of calling deleted lifecycle routes", async () => {
    api.getCharlieConnection.mockResolvedValue({ connected: false });
    api.getCharlieAgent.mockResolvedValue({
      applicationState: "not_installed",
      desiredReplicas: 0,
      readyReplicas: 0,
      standbyReplicas: [],
      replicas: [],
    });
    renderWithClient(<AgentTab />);
    expect(await screen.findByText(/installed from Charlie when you connect/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /install|upgrade|rollback|rotate|uninstall/i })).toBeNull();
  });

  it("keeps Charlie-owned intelligence configuration out of Astronomer", async () => {
    api.getCharlieActivation.mockResolvedValue({ activated: false });
    renderWithClient(<ConnectionTab />);
    expect(await screen.findByText(/Model providers, knowledge packs, and routes stay in Charlie/i)).toBeInTheDocument();
    expect(screen.getByText(/not a durable Charlie API key/i)).toBeInTheDocument();
  });

  it("accepts a rediscovered catalog on Astronomer so mode can be raised", async () => {
    const candidate = "a".repeat(64);
    api.getCharlieMode.mockResolvedValue({
      requested: "disabled",
      authoritative: "disabled",
      revision: 4,
      emergencyDisabled: false,
      disablePending: false,
      effects: ["Configuration, diagnostics, and audit remain available"],
      workloadCeiling: "disabled",
      workloadCeilingReady: true,
    });
    api.getCharlieKubernetesVisibility.mockResolvedValue({
      schema: "charlie.kubernetes-visibility/v1",
      profile: "product_namespace",
      revision: 4,
      state: "enabled",
      instanceId: "astronomer-management-plane",
      namespaces: ["astronomer"],
      productOwnedOnly: true,
      clusterScoped: false,
      podLogs: false,
      downstreamTargets: false,
      secretValues: false,
      exec: false,
      attach: false,
      portForward: false,
      apiProxy: false,
      requiresRediscovery: false,
      requiresCentralReview: true,
      requiresProductAcknowledgement: false,
      candidateDisclosureDigest: candidate,
      availableProfiles: ["disabled", "product_namespace", "cluster_diagnostics"],
      scopeSummary: "Product-owned resources in the Astronomer management namespace",
    });
    api.acknowledgeCharlieDisclosure.mockResolvedValue({
      requested: "disabled",
      authoritative: "disabled",
      revision: 4,
      emergencyDisabled: false,
      disclosureDigest: candidate,
      acknowledgedDisclosureDigest: candidate,
      effects: [],
      workloadCeiling: "disabled",
      workloadCeilingReady: true,
    });
    renderWithClient(<ModeTab />);
    expect(await screen.findByRole("button", { name: "Accept rediscovered catalog" })).toBeEnabled();
    expect(screen.getByRole("button", { name: /^read only/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Accept rediscovered catalog" }));
    await waitFor(() =>
      expect(api.acknowledgeCharlieDisclosure).toHaveBeenCalledWith(candidate),
    );
  });

  it("explains every authority mode and blocks elevation until disclosure acknowledgement", async () => {
    renderWithClient(<ModeTab />);
    expect(await screen.findByText("Every write requires exact approval")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^read only/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^approval required/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^automation/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^disabled/i })).toBeEnabled();
    expect(screen.getAllByText("Approval required").length).toBeGreaterThan(1);
    expect(screen.queryByText("auto", { exact: true })).toBeNull();
    expect(screen.getByRole("button", { name: "Emergency Disable" })).toBeEnabled();
    expect(screen.getByText(/Accept the rediscovered catalog/)).toHaveAttribute("role", "status");
    expect(screen.getByRole("button", { name: "Accept rediscovered catalog" })).toBeEnabled();
  });

  it("uses product mode labels while sending stable wire values", async () => {
    api.getCharlieMode.mockResolvedValue({
      requested: "read_only",
      authoritative: "read_only",
      revision: 4,
      emergencyDisabled: false,
      disablePending: false,
      disclosureDigest: digest("d"),
      acknowledgedDisclosureDigest: digest("d"),
      effects: ["Authorized reads only"],
      autoReadiness: { ready: true, blockers: [] },
      workloadCeiling: "read_only",
      workloadCeilingReady: true,
    });
    api.updateCharlieMode.mockResolvedValue({
      requested: "auto",
      authoritative: "auto",
      revision: 5,
      emergencyDisabled: false,
      effects: [],
      autoReadiness: { ready: true, blockers: [] },
      workloadCeiling: "auto",
      workloadCeilingReady: true,
    });
    renderWithClient(<ModeTab />);
    fireEvent.click(await screen.findByRole("button", { name: /^automation/i }));
    expect(
      screen.getAllByText(/Includes Read only and Approval required/i).length,
    ).toBeGreaterThan(0);
    expect(screen.getByTestId("charlie-mode-confirm-allowed")).toHaveTextContent(
      /What Automation allows/i,
    );
    fireEvent.change(screen.getByPlaceholderText("CHANGE TO AUTOMATION"), {
      target: { value: "CHANGE TO AUTOMATION" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Change mode" }));
    await waitFor(() =>
      expect(api.updateCharlieMode).toHaveBeenCalledWith("auto", 4),
    );
    expect(await screen.findByTestId("charlie-mode-transition")).toHaveAttribute(
      "data-phase",
      "ready",
    );
    expect(screen.getByText(/Mode ready for work/i)).toBeInTheDocument();
  });

  it("does not call a pending Charlie confirmation an unverified agent ceiling", async () => {
    api.getCharlieMode.mockResolvedValue({
      requested: "read_only",
      authoritative: "disabled",
      revision: 4,
      emergencyDisabled: false,
      disablePending: false,
      disclosureDigest: digest("d"),
      acknowledgedDisclosureDigest: digest("d"),
      effects: ["Fail closed"],
      autoReadiness: { ready: false, blockers: [] },
      workloadCeiling: "read_only",
      workloadCeilingReady: true,
    });
    renderWithClient(<ModeTab />);
    expect(
      await screen.findByText("Mode change is not yet confirmed"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /Both product-agent replicas already report Read only/,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("Agent ceiling not fully verified")).toBeNull();
  });

  it("shows validating state until agent ceiling is ready after mode change", async () => {
    api.getCharlieMode.mockResolvedValue({
      requested: "approval",
      authoritative: "approval",
      revision: 4,
      emergencyDisabled: false,
      disablePending: false,
      disclosureDigest: digest("d"),
      acknowledgedDisclosureDigest: digest("d"),
      effects: ["Every write requires exact approval"],
      autoReadiness: { ready: true, blockers: [] },
      workloadCeiling: "approval",
      workloadCeilingReady: true,
    });
    api.updateCharlieMode.mockResolvedValue({
      requested: "read_only",
      authoritative: "read_only",
      revision: 5,
      emergencyDisabled: false,
      effects: ["Authorized reads only"],
      workloadCeiling: "read_only",
      workloadCeilingReady: false,
    });
    api.getCharlieAgent.mockResolvedValue({
      applicationState: "degraded",
      desiredReplicas: 2,
      readyReplicas: 1,
      leaderReplica: "instance-0",
      standbyReplicas: [],
      replicas: [
        { ordinal: 0, instanceId: "instance-0", role: "leader", state: "ready" },
        { ordinal: 1, role: "standby", state: "unavailable" },
      ],
    });
    renderWithClient(<ModeTab />);
    fireEvent.click(await screen.findByRole("button", { name: /^read only/i }));
    fireEvent.change(screen.getByPlaceholderText("CHANGE TO READ ONLY"), {
      target: { value: "CHANGE TO READ ONLY" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Change mode" }));
    await waitFor(() =>
      expect(api.updateCharlieMode).toHaveBeenCalledWith("read_only", 4),
    );
    expect(await screen.findByTestId("charlie-mode-transition")).toHaveAttribute(
      "data-phase",
      "verifying",
    );
    expect(screen.getByText(/Validating product agents/i)).toBeInTheDocument();
  });

  it("renders trigger rules when Charlie returns null scopes", async () => {
    api.getCharlieAutomation.mockResolvedValue({
      defaultsRevision: 3,
      serviceIdentityEnabled: true,
      actionPolicies: [],
      rules: [{
        id: "rule-null-scopes",
        name: "agent_disconnected",
        enabled: false,
        sourceType: "agent_disconnected",
        severities: ["warning"],
        scopes: null as unknown as string[],
        cooldownSeconds: 1800,
        gracePeriodSeconds: 300,
        flapWindowSeconds: 300,
        flapCount: 1,
        fleetThresholdPercent: 0,
        suppressed: false,
        maximumAttempts: 8,
        deadLetterEnabled: true,
        serviceIdentity: "system:charlie-automation",
        modeCeiling: "read_only",
      }],
    });
    renderWithClient(<AutomationTab />);
    expect(await screen.findByLabelText("Rule name")).toHaveValue("agent_disconnected");
    expect(screen.getByLabelText("Scopes (comma separated)")).toHaveValue("");
    expect(screen.queryByText(/Automation configuration unavailable/i)).toBeNull();
  });

  it("exposes all automation policy fields and validates new rules before save", async () => {
    renderWithClient(<AutomationTab />);
    expect(await screen.findByText("astronomer.queue.retry_task")).toBeInTheDocument();
    expect(screen.getByText("exact session resource ID")).toBeInTheDocument();
    expect(screen.getByText(/task reaches a terminal state/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Max actions per window"), {
      target: { value: "4" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save action policy" }));
    await waitFor(() =>
      expect(api.updateCharlieActionPolicy).toHaveBeenCalledWith(
        expect.objectContaining({
          capability: "astronomer.queue.retry_task",
          maxActionsPerWindow: 4,
        }),
        expect.anything(),
      ),
    );
    expect(screen.getByLabelText("Rule name")).toHaveValue("Agent flap");
    expect(screen.getByLabelText("Severities (comma separated)")).toHaveValue("high");
    expect(screen.getByLabelText("Cluster coverage threshold %")).toHaveAttribute("max", "100");
    fireEvent.click(screen.getByRole("button", { name: "Add trigger rule" }));
    expect(screen.getByRole("alert")).toHaveTextContent("needs a name");
    expect(screen.getByRole("button", { name: "Save automation" })).toBeDisabled();
  });

  it("fails closed when central action-policy verification is unavailable", async () => {
    api.getCharlieAutomation.mockResolvedValue({
      defaultsRevision: 3,
      serviceIdentityEnabled: true,
      rules: [],
      actionPolicies: [{
        capability: "astronomer.queue.retry_task", effect: "retry one failed task", risk: "low",
        autoEligible: true, centralAllowlisted: false, centralState: "unavailable", enabled: false, revision: 2,
        maxActionsPerIncident: 1, maxActionsPerWindow: 3, budgetWindowSeconds: 3600,
        cooldownSeconds: 60, scopeSummary: "exact session resource ID", preconditions: [],
        verification: "task reaches a terminal state", circuitState: "unknown",
      }],
    });
    renderWithClient(<AutomationTab />);
    expect(await screen.findByLabelText("Enable astronomer.queue.retry_task")).toBeDisabled();
    expect(screen.getByText(/until Charlie central is verified/i)).toBeInTheDocument();
  });

  it("shows action-policy conflicts without claiming the change was saved", async () => {
    api.updateCharlieActionPolicy.mockRejectedValue(
      new Error("This action policy conflicts with current central allowlisting or bounded budget rules."),
    );
    renderWithClient(<AutomationTab />);
    fireEvent.change(await screen.findByLabelText("Max actions per window"), {
      target: { value: "4" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save action policy" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/conflicts with current central allowlisting/i);
  });

  it("shows only bounded dead-letter metadata and requires typed retry confirmation", async () => {
    api.listCharlieTriggerEvents.mockResolvedValue([{
      id: "22222222-2222-4222-8222-222222222222",
      ruleId: "rule-a",
      eventType: "agent.unhealthy",
      resourceType: "cluster",
      resourceId: "cluster-a",
      state: "dead",
      repeatCount: 1,
      attemptCount: 3,
      lastErrorCode: "delivery_failed",
      firstOccurredAt: "2026-08-05T09:00:00Z",
      lastOccurredAt: "2026-08-05T09:05:00Z",
      deadLetteredAt: "2026-08-05T09:10:00Z",
      updatedAt: "2026-08-05T09:10:00Z",
      payload: "dead-letter-payload-dom-canary",
    }]);
    api.retryCharlieTriggerEvent.mockResolvedValue({ id: "retry-a", state: "retry" });
    const view = renderWithClient(<AutomationTab />);
    expect(await screen.findByText("agent.unhealthy")).toBeInTheDocument();
    expect(screen.getByText("delivery_failed")).toBeInTheDocument();
    expect(view.container.textContent).not.toContain("dead-letter-payload-dom-canary");

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    const confirm = screen.getByRole("button", { name: "Queue retry" });
    expect(confirm).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("RETRY TRIGGER"), {
      target: { value: "RETRY TRIGGER" },
    });
    fireEvent.click(confirm);
    await waitFor(() =>
      expect(api.retryCharlieTriggerEvent).toHaveBeenCalledWith(
        "22222222-2222-4222-8222-222222222222",
        expect.anything(),
      ),
    );
  });

  it("renders every independent diagnostic without making it core readiness", async () => {
    api.getCharlieDiagnostics.mockResolvedValue({
      overall: "degraded",
      correlationId: "correlation-a",
      checks: [{
        id: "product_bridge_mtls",
        label: "Product bridge mTLS",
        state: "degraded",
        summary: "Certificate expires soon",
        nextAction: "Rotate the product bridge certificate",
      }],
    });
    renderWithClient(<DiagnosticsTab />);
    expect(await screen.findByText("Local database and configuration")).toBeInTheDocument();
    expect(screen.getByText("Agent standby replica")).toBeInTheDocument();
    expect(screen.getByText(/never participates in Astronomer's core readiness/i)).toBeInTheDocument();
    expect(screen.getByText("correlation-a")).toBeInTheDocument();
    expect(screen.getByText(/Next action: Rotate the product bridge certificate/i)).toBeInTheDocument();
  });
});
