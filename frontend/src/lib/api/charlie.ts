import api from "@/lib/api";
import { API_BASE } from "@/lib/env";
export type CharlieResource = {
  type:
    | "installation"
    | "management_component"
    | "alert"
    | "backup"
    | "self_management_application"
    | "agent_connection_record"
    | "agent_fleet"
    | "tunnel";
  id: string;
  requiredVerb: "read";
};
export type CharlieSession = {
  id: string;
  clientSessionId: string;
  intent: string;
  resourceScopeSummary: string;
  state:
    | "creating"
    | "active"
    | "waiting_approval"
    | "completed"
    | "aborted"
    | "failed";
  visibility: "private" | "incident";
  centralRevision: number;
  source: "user" | "event";
  createdAt?: string;
  updatedAt?: string;
};

export interface CharlieContextOption extends CharlieResource {
  label: string;
  summary: string;
}
export interface CharlieCitation {
  id: string;
  title: string;
  href?: string;
  source: string;
}
export interface CharlieToolRun {
  id: string;
  capability: string;
  effect: string;
  risk: string;
  /** Server-produced field-name-only summary. Exact argument values never reach the browser. */
  argumentSummary?: string[];
  state: string;
  result?: string;
  auditCorrelationId?: string;
}
export interface CharlieApproval {
  id: string;
  title: string;
  state: "pending" | "approved" | "denied" | "expired";
  eligible: boolean;
  capability: string;
  target: string;
  risk: string;
  effect?: string;
  requiredPermission?: string;
  expiresAt?: string;
  reason?: string;
}
export interface CharlieMessage {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  state?: string;
  retrieval?: {
    state: "searching" | "complete" | "partial" | "failed";
    documentCount?: number;
    summary?: string;
  };
  citations?: CharlieCitation[];
  tools?: CharlieToolRun[];
  approval?: CharlieApproval;
  createdAt?: string;
}
type CharlieHistoryItemWire = {
  item_id?: string;
  itemId?: string;
  kind: "user_message" | "assistant_message" | "finding_evidence";
  redacted_content?: string;
  redactedContent?: string;
  created_at?: string;
  createdAt?: string;
};
export interface CharlieFinding {
  id: string;
  title: string;
  severity: "low" | "medium" | "warning" | "high" | "critical";
  state: "open" | "acknowledged" | "dismissed" | "resolved" | "expired";
  affectedResource: CharlieResource;
  confidence?: number;
  reasonNoAction?: string;
  summary: string;
  sessionId?: string;
  source?: string;
  repeatCount?: number;
  createdAt?: string;
  updatedAt?: string;
  workflowState:
    | "approval_pending"
    | "manual_remediation_required"
    | "remediation_in_progress"
    | "verification_pending"
    | "resolved"
    | "rejected"
    | "dismissed"
    | "expired";
  availableDecisions: Array<
    | "open_exact_approval"
    | "reject_exact_approval"
    | "acknowledge"
    | "start_remediation"
    | "request_verification"
    | "dismiss"
    | "resolve"
  >;
  evidence?: Array<{
    label: string;
    summary: string;
    citation?: CharlieCitation;
  }>;
  operatorChecks?: string[];
  manualRemediation?: {
    preconditions: string[];
    steps: string[];
    expectedImpact: string;
    rollback?: string;
    verificationMethod: string;
    verificationSteps: string[];
  };
  proposedAction?: {
    capability: string;
    target: string;
    risk: string;
    impact: string;
    preconditions: string[];
    expectedResult?: string;
    rollback?: string;
    verification?: string;
    mode: string;
    eligible: boolean;
    approvalId?: string;
  };
}

type CharlieWireResource = {
  type: CharlieResource["type"];
  id: string;
  requiredVerb?: "read";
  required_verb?: "read";
};
type CharlieWireSession = {
  id: string;
  clientSessionId?: string;
  client_session_id?: string;
  intent: string;
  resourceScopeSummary?: string;
  resource_scope_summary?: string;
  state: CharlieSession["state"];
  visibility: CharlieSession["visibility"];
  centralRevision?: number;
  central_revision?: number;
  source?: CharlieSession["source"];
  createdAt?: string;
  created_at?: string;
  updatedAt?: string;
  updated_at?: string;
};

interface CharlieCentralFinding {
  recommendedCapability?: string;
  recommended_capability?: string;
  blockCode?: string;
  block_code?: string;
  riskImpact?: string;
  risk_impact?: string;
  preconditions?: string[];
  rollback?: string;
  expectedResult?: string;
  expected_result?: string;
  verificationSteps?: string[];
  verification_steps?: string[];
  confidence?: number;
  diagnosis?: string;
  evidenceSummary?: string[];
  evidence_summary?: string[];
  operatorChecks?: string[];
  operator_checks?: string[];
  workflow?: {
    state?: CharlieFinding["workflowState"];
    manualRemediation?: CharlieManualRemediationWire;
    manual_remediation?: CharlieManualRemediationWire;
  };
}
interface CharlieManualRemediationWire {
  preconditions?: string[];
  steps?: string[];
  expectedImpact?: string;
  expected_impact?: string;
  rollback?: string;
  verification?: { method?: string; steps?: string[] };
}
interface CharlieProposedActionWire {
  label?: string;
  mode?: string;
  eligible?: boolean;
  approvalId?: string;
  approval_id?: string;
}
interface CharlieFindingWire {
  id: string;
  title: string;
  severity: "info" | CharlieFinding["severity"];
  state: CharlieFinding["state"];
  summary?: string;
  affectedResource?: CharlieWireResource;
  affected_resource?: CharlieWireResource;
  reasonNoAction?: string;
  reason_no_action?: string;
  riskImpact?: string;
  risk_impact?: string;
  verificationSummary?: string;
  verification_summary?: string;
  proposedAction?: CharlieProposedActionWire;
  proposed_action?: CharlieProposedActionWire;
  detail?: { finding?: CharlieCentralFinding };
  sessionId?: string;
  session_id?: string;
  source?: string;
  repeatCount?: number;
  repeat_count?: number;
  createdAt?: string;
  created_at?: string;
  updatedAt?: string;
  updated_at?: string;
  workflowState?: CharlieFinding["workflowState"];
  workflow_state?: CharlieFinding["workflowState"];
  availableDecisions?: CharlieFinding["availableDecisions"];
  available_decisions?: CharlieFinding["availableDecisions"];
}

function mapCharlieSession(value: CharlieWireSession): CharlieSession {
  return {
    id: value.id,
    clientSessionId: value.clientSessionId ?? value.client_session_id ?? "",
    intent: value.intent,
    resourceScopeSummary:
      value.resourceScopeSummary ?? value.resource_scope_summary ?? "",
    state: value.state,
    visibility: value.visibility,
    centralRevision: value.centralRevision ?? value.central_revision ?? 0,
    source: value.source ?? (value.visibility === "incident" ? "event" : "user"),
    createdAt: value.createdAt ?? value.created_at,
    updatedAt: value.updatedAt ?? value.updated_at,
  };
}

function mapCharlieResource(value: CharlieWireResource): CharlieResource {
  return {
    type: value.type,
    id: value.id,
    requiredVerb: value.requiredVerb ?? value.required_verb ?? "read",
  };
}

function mapCharlieFinding(value: CharlieFindingWire): CharlieFinding {
  const central = value.detail?.finding ?? {};
  const affected = value.affectedResource ??
    value.affected_resource ?? {
      type: "installation",
      id: "unknown",
      requiredVerb: "read",
    };
  const severity = value.severity === "info" ? "low" : value.severity;
  const proposedWire = value.proposedAction ?? value.proposed_action;
  const manualWire =
    central.workflow?.manualRemediation ??
    central.workflow?.manual_remediation;
  const recommended =
    central.recommendedCapability ?? central.recommended_capability;
  const proposed =
    proposedWire || recommended
      ? {
          capability: recommended ?? proposedWire?.label ?? "operator.review",
          target: affected?.id ?? "installation",
          risk:
            central.blockCode ??
            central.block_code ??
            value.reasonNoAction ??
            value.reason_no_action ??
            "review_required",
          impact:
            central.riskImpact ??
            central.risk_impact ??
            value.riskImpact ??
            value.risk_impact ??
            "Review the bounded recommendation before proceeding.",
          preconditions: central.preconditions ?? [],
          rollback: central.rollback,
          expectedResult: central.expectedResult ?? central.expected_result,
          verification:
            ((
              central.verificationSteps ??
              central.verification_steps ??
              []
            ).join("; ") ||
              value.verificationSummary) ??
            value.verification_summary,
          mode: proposedWire?.mode ?? "read_only",
          eligible: proposedWire?.eligible === true,
          approvalId: proposedWire?.approvalId ?? proposedWire?.approval_id,
        }
      : undefined;
  return {
    id: value.id,
    title: value.title,
    severity,
    state: value.state,
    affectedResource: mapCharlieResource(affected),
    confidence: central.confidence,
    reasonNoAction: value.reasonNoAction ?? value.reason_no_action,
    summary: value.summary || central.diagnosis || "",
    sessionId: value.sessionId ?? value.session_id,
    source: value.source,
    repeatCount: value.repeatCount ?? value.repeat_count ?? 1,
    createdAt: value.createdAt ?? value.created_at,
    updatedAt: value.updatedAt ?? value.updated_at,
    workflowState:
      value.workflowState ?? value.workflow_state ?? "manual_remediation_required",
    availableDecisions:
      value.availableDecisions ?? value.available_decisions ?? [],
    evidence: (central.evidenceSummary ?? central.evidence_summary ?? []).map(
      (summary: string, index: number) => ({
        label: `Evidence ${index + 1}`,
        summary,
      }),
    ),
    operatorChecks: central.operatorChecks ?? central.operator_checks ?? [],
    manualRemediation: manualWire
      ? {
          preconditions: manualWire.preconditions ?? [],
          steps: manualWire.steps ?? [],
          expectedImpact:
            manualWire.expectedImpact ?? manualWire.expected_impact ?? "",
          rollback: manualWire.rollback,
          verificationMethod: manualWire.verification?.method ?? "",
          verificationSteps: manualWire.verification?.steps ?? [],
        }
      : undefined,
    proposedAction: proposed,
  };
}

export async function getCharlieOverview(): Promise<{
  sessions: CharlieSession[];
  mode: "disabled" | "read_only" | "approval" | "auto";
}> {
  const { data } = await api.get("/charlie/sessions/");
  const value = data.data ?? data;
  return {
    sessions: (value.sessions ?? []).map(mapCharlieSession),
    mode: value.mode ?? "disabled",
  };
}
export async function listCharlieSessions(): Promise<CharlieSession[]> {
  return (await getCharlieOverview()).sessions;
}
export async function createCharlieSession(input: {
  clientSessionId: string;
  intent: string;
  trigger?: string;
  currentUiContext?: string;
  resources?: CharlieResource[];
}): Promise<CharlieSession> {
  const { data } = await api.post("/charlie/sessions/", {
    client_session_id: input.clientSessionId,
    intent: input.intent,
    trigger: input.trigger,
    current_ui_context: input.currentUiContext,
    resources: input.resources?.map((r) => ({
      type: r.type,
      id: r.id,
      required_verb: r.requiredVerb,
    })),
  });
  return mapCharlieSession(data.session ?? data.data?.session ?? data);
}
export async function getCharlieHistory(id: string): Promise<CharlieMessage[]> {
  const { data } = await api.get(
    `/charlie/sessions/${encodeURIComponent(id)}/history/`,
  );
  const value = data.messages ?? data.data?.messages ?? data.data ?? data;
  if (!Array.isArray(value)) return [];
  return (value as CharlieHistoryItemWire[]).map((item) => ({
    id: item.itemId ?? item.item_id ?? "",
    role:
      item.kind === "user_message"
        ? "user"
        : item.kind === "assistant_message"
          ? "assistant"
          : "system",
    content: item.redactedContent ?? item.redacted_content ?? "",
    createdAt: item.createdAt ?? item.created_at,
  }));
}
export async function sendCharlieMessage(id: string, message: string) {
  const { data } = await api.post(
    `/charlie/sessions/${encodeURIComponent(id)}/messages/`,
    { client_message_id: crypto.randomUUID(), message },
  );
  return data;
}
export async function abortCharlieSession(id: string) {
  await api.post(`/charlie/sessions/${encodeURIComponent(id)}/abort/`, {
    request_id: crypto.randomUUID(),
  });
}

const charlieSessionEventTypes = [
  "turn.started",
  "text.delta",
  "tool.proposed",
  "tool.running",
  "tool.succeeded",
  "tool.failed",
  "permission.requested",
  "permission.responded",
  "turn.completed",
  "turn.failed",
  "turn.aborted",
  "charlie.error",
] as const;

// EventSource keeps its last confirmed event ID and sends Last-Event-ID on an
// automatic reconnect. Astronomer also persists the cursor after flushing each
// event, so a new browser/server can resume without storing conversation data.
export function subscribeCharlieSessionEvents(
  id: string,
  onEvent: (event: MessageEvent<string>) => void,
  onError: () => void,
): () => void {
  const base = API_BASE.replace(/\/$/, "");
  const source = new EventSource(
    `${base}/charlie/sessions/${encodeURIComponent(id)}/events/`,
    { withCredentials: true },
  );
  const listener = (event: Event) => onEvent(event as MessageEvent<string>);
  for (const type of charlieSessionEventTypes) {
    source.addEventListener(type, listener);
  }
  source.onerror = onError;
  return () => {
    for (const type of charlieSessionEventTypes) {
      source.removeEventListener(type, listener);
    }
    source.close();
  };
}

// Optional gateway surfaces. A 404/403 is rendered as an unavailable/permission
// state; the client never substitutes local authorization or execution.
export async function searchCharlieContext(
  query: string,
): Promise<CharlieContextOption[]> {
  const { data } = await api.get("/charlie/context/search/", {
    params: { q: query, limit: 20 },
  });
  return data.items ?? data.data?.items ?? [];
}
export async function listCharlieFindings(): Promise<CharlieFinding[]> {
  const { data } = await api.get("/charlie/findings/", {
    params: { limit: 100 },
  });
  return (data.items ?? data.data?.items ?? []).map(mapCharlieFinding);
}
export async function getCharlieFinding(id: string): Promise<CharlieFinding> {
  const { data } = await api.get(
    `/charlie/findings/${encodeURIComponent(id)}/`,
  );
  return mapCharlieFinding(data.finding ?? data.data?.finding ?? data);
}
export async function transitionCharlieFinding(
  id: string,
  action:
    | "acknowledge"
    | "start_remediation"
    | "request_verification"
    | "dismiss"
    | "resolve",
) {
  const path = action.replaceAll("_", "-");
  await api.post(`/charlie/findings/${encodeURIComponent(id)}/${path}/`, {
    request_id: crypto.randomUUID(),
  });
}
export async function listCharlieApprovals(): Promise<CharlieApproval[]> {
  const { data } = await api.get("/charlie/approvals/");
  return data.items ?? data.data?.items ?? [];
}
export async function decideCharlieApproval(
  id: string,
  decision: "approve" | "deny",
  rationale = "",
) {
  try {
    await api.post(`/charlie/approvals/${encodeURIComponent(id)}/decision/`, {
      request_id: crypto.randomUUID(),
      decision,
      rationale: rationale.trim().slice(0, 512),
    });
  } catch (error) {
    const status = (error as { response?: { status?: number } }).response?.status;
    if (status === 409) {
      throw new Error("This exact approval is stale or was already decided. Refresh before trying again.");
    }
    if (status === 403) {
      throw new Error("Approval eligibility or target permission changed. No action was authorized.");
    }
    throw new Error("Charlie could not confirm the decision. No action was authorized.");
  }
}
