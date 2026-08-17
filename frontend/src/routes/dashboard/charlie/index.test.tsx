import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { Suspense, type ReactNode } from "react";
import type { User } from "@/types";
import type {
  CharlieApproval,
  CharlieFinding,
  CharlieSession,
} from "@/lib/api/charlie";
import {
  decideCharlieApproval,
  getCharlieFinding,
  getCharlieOverview,
  getCharlieThreadHistory,
  listCharlieApprovals,
  listCharlieFindings,
  listCharlieSessions,
  listCharlieThreads,
  transitionCharlieFinding,
} from "@/lib/api/charlie";
import { useAuthStore } from "@/lib/store";
import { Route } from "./index";

const CharlieHub = Route.options.component!;

beforeAll(async () => {
  await (CharlieHub as typeof CharlieHub & { preload?: () => Promise<unknown> }).preload?.();
});

let search = new URLSearchParams();
const push = vi.fn();

vi.mock("@/lib/navigation", () => ({
  useSearchParams: () => search,
  useRouter: () => ({ push }),
}));
vi.mock("@/lib/link", () => ({
  Link: ({ href, children, ...props }: { href: string; children: ReactNode }) => (
    <a href={href} {...props}>{children}</a>
  ),
}));
vi.mock("@/lib/api/charlie", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/charlie")>()),
  decideCharlieApproval: vi.fn(),
  getCharlieFinding: vi.fn(),
  getCharlieOverview: vi.fn(),
  getCharlieThreadHistory: vi.fn(),
  listCharlieApprovals: vi.fn(),
  listCharlieFindings: vi.fn(),
  listCharlieSessions: vi.fn(),
  listCharlieThreads: vi.fn(),
  transitionCharlieFinding: vi.fn(),
}));

const incident: CharlieSession = {
  id: "incident-1",
  clientSessionId: "client-incident",
  intent: "Investigate flapping agent",
  resourceScopeSummary: "agent connection record agent-east",
  state: "active",
  visibility: "incident",
  source: "event",
  centralRevision: 3,
  createdAt: "2026-08-06T10:00:00Z",
};
const privateSession: CharlieSession = {
  ...incident,
  id: "private-1",
  clientSessionId: "client-private",
  intent: "Private operator question",
  visibility: "private",
  source: "user",
};
const finding: CharlieFinding = {
  id: "finding-1",
  title: "Agent connection is flapping",
  severity: "high",
  state: "open",
  affectedResource: {
    type: "agent_connection_record",
    id: "agent-east",
    requiredVerb: "read",
  },
  summary: "Three reconnects occurred inside the configured window.",
  sessionId: incident.id,
  source: "cluster_agents",
  repeatCount: 3,
  reasonNoAction: "authority.read_only_write",
  createdAt: "2026-08-06T10:00:00Z",
  updatedAt: "2026-08-06T10:10:00Z",
  workflowState: "manual_remediation_required",
  availableDecisions: ["acknowledge", "start_remediation", "dismiss"],
  operatorChecks: ["Check the Astronomer server connection history."],
  manualRemediation: {
    preconditions: ["Confirm the connection remains unstable."],
    steps: ["Inspect management-plane ingress and tunnel locator health."],
    expectedImpact: "No downstream cluster access.",
    verificationMethod: "product_readback",
    verificationSteps: ["Verify the heartbeat is current."],
  },
};

function renderHub() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <Suspense fallback={<p>Loading Charlie test page</p>}>
        <CharlieHub />
      </Suspense>
    </QueryClientProvider>,
  );
}

describe("Charlie hub acceptance", () => {
  beforeEach(() => {
    search = new URLSearchParams();
    push.mockReset();
    vi.clearAllMocks();
    vi.mocked(listCharlieSessions).mockResolvedValue([privateSession, incident]);
    vi.mocked(listCharlieFindings).mockResolvedValue([finding]);
    vi.mocked(getCharlieFinding).mockResolvedValue(finding);
    vi.mocked(getCharlieOverview).mockResolvedValue({
      sessions: [privateSession, incident],
      mode: "read_only",
    });
    vi.mocked(getCharlieThreadHistory).mockResolvedValue([]);
    vi.mocked(listCharlieThreads).mockResolvedValue([
      {
        id: privateSession.id,
        title: privateSession.intent,
        state: privateSession.state,
      },
    ]);
    vi.mocked(listCharlieApprovals).mockResolvedValue([]);
    vi.mocked(transitionCharlieFinding).mockResolvedValue(undefined);
    vi.mocked(decideCharlieApproval).mockResolvedValue(undefined);
    useAuthStore.setState({
      user: { id: "admin", isSuperuser: true } as unknown as User,
      isAuthenticated: true,
    });
  });

  it("keeps private conversations separate from shared investigations", async () => {
    renderHub();
    expect(await screen.findByText("Private operator question")).toBeInTheDocument();
    expect(screen.queryByText("Investigate flapping agent")).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "investigations" }));
    expect(push).toHaveBeenCalledWith("/dashboard/charlie?tab=investigations");
  });

  it("uses URL-backed investigation filters and links only the originating product resource", async () => {
    search = new URLSearchParams("tab=investigations&incident=incident-1&severity=high&cluster=agent-east&source=event&from=2026-08-01&to=2026-08-07");
    renderHub();
    expect(await screen.findAllByText("Investigate flapping agent")).toHaveLength(2);
    expect(screen.getByLabelText("Investigation severity")).toHaveValue("high");
    expect(screen.getByLabelText("Agent connection record")).toHaveValue("agent-east");
    expect(screen.getByText("Repeated 3 times.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open originating agent connection record/ })).toHaveAttribute(
      "href",
      "/dashboard/agents?connection=agent-east",
    );
    fireEvent.change(screen.getByLabelText("Investigation severity"), { target: { value: "critical" } });
    expect(push).toHaveBeenCalledWith(expect.stringContaining("severity=critical"));
    expect(push).toHaveBeenCalledWith(expect.stringContaining("cluster=agent-east"));
  });

  it("shows a blocked finding's exact reason and only its server-authorized lifecycle decisions", async () => {
    search = new URLSearchParams("tab=findings&finding=finding-1&status=open&severity=high&source=cluster_agents&resource=agent-east&block=authority.read_only_write&from=2026-08-01&to=2026-08-07");
    renderHub();
    expect(await screen.findByText("No action: authority.read_only_write")).toBeInTheDocument();
    expect(screen.getByText(/No Charlie action is authorized/)).toBeInTheDocument();
    expect(screen.getByText("Operator checks")).toBeInTheDocument();
    expect(screen.getByText("Manual remediation")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "acknowledge" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "start remediation" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "dismiss" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /approve/i })).toBeNull();
    expect(decideCharlieApproval).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "acknowledge" }));
    await waitFor(() => expect(transitionCharlieFinding).toHaveBeenCalledWith("finding-1", "acknowledge"));
    expect(decideCharlieApproval).not.toHaveBeenCalled();
  });

  it("requires exact confirmation and preserves bounded rationale for one eligible approval", async () => {
    const approval: CharlieApproval = {
      id: "approval-1",
      title: "Restart one server replica",
      state: "pending",
      eligible: true,
      capability: "astronomer.server.restart_replica",
      target: "management_component:server-a",
      risk: "medium",
	      effect: "write",
      requiredPermission: "management_components:update",
      expiresAt: "2026-08-06T12:00:00Z",
	      review: {
	        description: "Restart one unhealthy management-plane replica",
	        expectedImpact: "One server replica reconnects",
	        reversible: true,
	        rollback: "Stop the rollout and restore the prior replica",
	        destructive: false,
	        argumentsWithheld: true,
	      },
    };
    vi.mocked(listCharlieApprovals).mockResolvedValue([approval]);
    search = new URLSearchParams("tab=approvals&approval=approval-1");
    renderHub();
    expect(await screen.findByText("Restart one server replica")).toBeInTheDocument();
    expect(screen.getByText("management_components:update")).toBeInTheDocument();
	    expect(screen.getByText("Restart one unhealthy management-plane replica")).toBeInTheDocument();
	    expect(screen.getByText("One server replica reconnects")).toBeInTheDocument();
	    expect(screen.getByText("Withheld by Charlie")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Rationale for Restart one server replica"), {
      target: { value: "Health probe remains unhealthy" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Review approval" }));
    expect(decideCharlieApproval).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Approve exact action" }));
    await waitFor(() => expect(decideCharlieApproval).toHaveBeenCalledWith(
      "approval-1",
      "approve",
      "Health probe remains unhealthy",
    ));
  });

  it.each(["approved", "denied", "expired"] as const)(
    "never resubmits a terminal %s approval",
    async (state) => {
      vi.mocked(listCharlieApprovals).mockResolvedValue([{
        id: `approval-${state}`,
        title: `${state} approval`,
        state,
        eligible: true,
        capability: "astronomer.server.restart_replica",
        target: "management_component:server-a",
        risk: "medium",
      }]);
      search = new URLSearchParams("tab=approvals");
      renderHub();
      expect(await screen.findByText(`${state} approval`)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Review approval" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Review denial" })).toBeNull();
      expect(decideCharlieApproval).not.toHaveBeenCalled();
    },
  );

  it("removes every decision when live Charlie permission is absent", async () => {
    useAuthStore.setState({ user: { id: "viewer" } as User, isAuthenticated: true });
    search = new URLSearchParams("tab=findings&finding=finding-1");
    renderHub();
    expect(await screen.findByText(/Requires charlie:update/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "acknowledge" })).toBeNull();
    expect(transitionCharlieFinding).not.toHaveBeenCalled();
  });
});
