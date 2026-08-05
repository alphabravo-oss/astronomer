import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { User } from "@/types";

const api = vi.hoisted(() => ({
  acknowledgeCharlieDisclosure: vi.fn(),
  consumeCharlieOnboarding: vi.fn(),
  deleteCharlieAutomationRule: vi.fn(),
  disconnectCharlie: vi.fn(),
  emergencyDisableCharlie: vi.fn(),
  getCharlieAccess: vi.fn(),
  getCharlieAgent: vi.fn(),
  getCharlieAutomation: vi.fn(),
  getCharlieConnection: vi.fn(),
  getCharlieDiagnostics: vi.fn(),
  getCharlieMode: vi.fn(),
  listCharlieTriggerEvents: vi.fn(),
  retryCharlieTriggerEvent: vi.fn(),
  runCharlieAgentAction: vi.fn(),
  uninstallCharlieAgent: vi.fn(),
  updateCharlieAutomation: vi.fn(),
  updateCharlieActionPolicy: vi.fn(),
  updateCharlieMode: vi.fn(),
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
  AgentTab,
  AutomationTab,
  CharlieAdminContent,
  ConnectionTab,
  DiagnosticsTab,
  ModeTab,
  agentActionsForState,
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
  api.getCharlieMode.mockResolvedValue({
    requested: "approval",
    authoritative: "approval",
    revision: 4,
    emergencyDisabled: false,
    disablePending: false,
    disclosureDigest: digest("d"),
    acknowledgedDisclosureDigest: digest("c"),
    effects: ["Every write requires exact approval"],
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
      maximumAttempts: 3, deadLetterEnabled: true, serviceIdentity: "charlie-automation",
    }],
  });
  api.getCharlieAccess.mockResolvedValue({ effectivePermissions: [], automationGrants: [] });
  api.listCharlieTriggerEvents.mockResolvedValue([]);
  api.getCharlieDiagnostics.mockResolvedValue({ overall: "healthy", checks: [], correlationId: "correlation-a" });
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
});

afterEach(cleanup);

describe("Charlie administration acceptance", () => {
  it("covers disabled, loading, denied, and authorized feature/permission states", async () => {
    feature.value = { data: { "feature.charlie": false }, isError: false, refetch: vi.fn() };
    const disabled = renderWithClient(<CharlieAdminPage />);
    expect(screen.getByText("Charlie is disabled")).toBeInTheDocument();
    disabled.unmount();

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

  it("renders only verified safe onboarding metadata and never package credentials", async () => {
    const secret = "enrollment-secret-dom-canary";
    const artifactCredential = "artifact-secret-dom-canary";
    const signature = "signed-package-dom-canary";
    const certificatePrivateKey = "certificate-private-key-dom-canary";
    const runtimeToken = "runtime-token-dom-canary";
    const view = renderWithClient(<ConnectionTab />);
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

  it("shows truthful per-replica state and state-appropriate lifecycle actions", async () => {
    expect(agentActionsForState("not_installed")).toEqual(["install"]);
    expect(agentActionsForState("installing")).toEqual([]);
    expect(agentActionsForState("ready")).toEqual(["upgrade", "rollback", "rotate"]);
    renderWithClient(<AgentTab />);
    expect((await screen.findAllByText("instance-0")).length).toBeGreaterThan(0);
    expect(screen.getAllByText("instance-1").length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
    expect(screen.getByRole("button", { name: "Upgrade" })).toBeEnabled();
  });

  it("gates agent installation on a consumed signed connection", async () => {
    api.getCharlieConnection.mockResolvedValue({ connected: false });
    api.getCharlieAgent.mockResolvedValue({
      applicationState: "not_installed",
      desiredReplicas: 0,
      readyReplicas: 0,
      standbyReplicas: [],
      replicas: [],
    });
    renderWithClient(<AgentTab />);
    expect(await screen.findByRole("button", { name: "Install" })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent(
      /signed Charlie onboarding package has been validated and consumed/i,
    );
    expect(api.runCharlieAgentAction).not.toHaveBeenCalled();
  });

  it("keeps Charlie-owned intelligence configuration out of Astronomer", async () => {
    renderWithClient(<ConnectionTab />);
    expect(await screen.findByText(/Model providers, LLM routing, RAG/i)).toBeInTheDocument();
    expect(screen.getByText(/remain administered in the separate Charlie service/i)).toBeInTheDocument();
  });

  it("explains every authority mode and blocks elevation until disclosure acknowledgement", async () => {
    renderWithClient(<ModeTab />);
    expect(await screen.findByText("Every write requires exact approval")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /read only/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^approval Charlie/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^auto/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^disabled/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Emergency Disable" })).toBeEnabled();
    expect(screen.getByText(/Review and acknowledge/)).toHaveAttribute("role", "status");
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
    expect(screen.getByLabelText("Fleet threshold %")).toHaveAttribute("max", "100");
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
