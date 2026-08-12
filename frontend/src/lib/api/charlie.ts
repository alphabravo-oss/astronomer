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
  review?: {
    description?: string;
    expectedImpact?: string;
    reversible?: boolean;
    rollback?: string;
    destructive?: boolean;
    argumentsWithheld: true;
  };
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
  citations?: Array<{
    id?: unknown;
    title?: unknown;
    source?: unknown;
  }>;
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
  riskImpact?: string;
  verificationSummary?: string;
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
  proposedAction?: {
    capability: string;
    mode: "approval" | "auto";
    eligible: boolean;
    approvalId?: string;
  };
  manualRemediation?: {
    preconditions: string[];
    steps: string[];
    expectedImpact: string;
    rollback?: string;
    verificationMethod: string;
    verificationSteps: string[];
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

interface CharlieAdvisoryDetailWire {
  riskImpact?: string;
  risk_impact?: string;
  preconditions?: string[];
  rollback?: string;
  verificationSteps?: string[];
  verification_steps?: string[];
  confidence?: number;
  diagnosis?: string;
  evidenceSummary?: string[];
  evidence_summary?: string[];
  operatorChecks?: string[];
  operator_checks?: string[];
  manualRemediation?: CharlieManualRemediationWire;
  manual_remediation?: CharlieManualRemediationWire;
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
  detail?: CharlieAdvisoryDetailWire | { finding?: CharlieAdvisoryDetailWire };
  proposedAction?: {
    label?: string;
    capability?: string;
    mode?: "approval" | "auto";
    eligible?: boolean;
    approvalId?: string;
    approval_id?: string;
  };
  proposed_action?: CharlieFindingWire["proposedAction"];
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
  const detail = value.detail ?? {};
  const advisory: CharlieAdvisoryDetailWire = "finding" in detail
    ? (detail.finding ?? {})
    : (detail as CharlieAdvisoryDetailWire);
  const affected = value.affectedResource ??
    value.affected_resource ?? {
      type: "installation",
      id: "unknown",
      requiredVerb: "read",
    };
  const severity = value.severity === "info" ? "low" : value.severity;
  const manualWire = advisory.manualRemediation ?? advisory.manual_remediation ??
    advisory.workflow?.manualRemediation ?? advisory.workflow?.manual_remediation;
  const proposedAction = value.proposedAction ?? value.proposed_action;
  return {
    id: value.id,
    title: value.title,
    severity,
    state: value.state,
    affectedResource: mapCharlieResource(affected),
    confidence: advisory.confidence,
    reasonNoAction: value.reasonNoAction ?? value.reason_no_action,
    riskImpact: value.riskImpact ?? value.risk_impact ?? advisory.riskImpact ?? advisory.risk_impact,
    verificationSummary: value.verificationSummary ?? value.verification_summary,
    summary: value.summary || advisory.diagnosis || "",
    sessionId: value.sessionId ?? value.session_id,
    source: value.source,
    repeatCount: value.repeatCount ?? value.repeat_count ?? 1,
    createdAt: value.createdAt ?? value.created_at,
    updatedAt: value.updatedAt ?? value.updated_at,
    workflowState:
      value.workflowState ?? value.workflow_state ?? advisory.workflow?.state ?? "manual_remediation_required",
    availableDecisions:
      value.availableDecisions ?? value.available_decisions ?? [],
    evidence: (advisory.evidenceSummary ?? advisory.evidence_summary ?? []).map(
      (summary: string, index: number) => ({
        label: `Evidence ${index + 1}`,
        summary,
      }),
    ),
    operatorChecks: advisory.operatorChecks ?? advisory.operator_checks ?? [],
    proposedAction: proposedAction
      ? {
          capability: proposedAction.capability ?? proposedAction.label ?? "",
          mode: proposedAction.mode ?? "approval",
          eligible: proposedAction.eligible ?? false,
          approvalId: proposedAction.approvalId ?? proposedAction.approval_id,
        }
      : undefined,
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
  return (value as CharlieHistoryItemWire[]).map((item) => {
    const citations = (Array.isArray(item.citations) ? item.citations : [])
      .slice(0, 16)
      .flatMap((citation) => {
        const id = typeof citation.id === "string" ? citation.id : "";
        const title = typeof citation.title === "string" ? citation.title.trim() : "";
        const source = typeof citation.source === "string" ? citation.source.trim() : "";
        if (!id || !title || !source) return [];
        return [{ id: id.slice(0, 128), title: title.slice(0, 1024), source: source.slice(0, 2048) }];
      });
    return {
      id: item.itemId ?? item.item_id ?? "",
      role:
        item.kind === "user_message"
          ? "user"
          : item.kind === "assistant_message"
            ? "assistant"
            : "system",
      content: item.redactedContent ?? item.redacted_content ?? "",
      ...(citations.length ? { citations } : {}),
      createdAt: item.createdAt ?? item.created_at,
    };
  });
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

export type CharlieActiveThread = {
  thread: {
    id: string;
    title: string;
    state: string;
    current_session_id?: string | null;
    created_at?: string;
    updated_at?: string;
  } | null;
  messageable?: boolean;
  needs_continue?: boolean;
  session_ids?: string[];
  current_session?: CharlieSession | null;
};

export type CharlieTurnReceipt = {
  sessionId: string;
  turnId: string;
  acceptedAt?: string;
};

export type CharlieCommandDescriptor = {
  id: string;
  version: string;
  name: string;
  aliases?: string[];
  label: string;
  description: string;
  category: string;
  execution: "agent" | "client";
  effect: "read" | "local";
  required_mode: "read_only";
  example: string;
  argument?: {
    name: string;
    placeholder: string;
    required: boolean;
  };
};

export type CharlieCommandCatalog = {
  schema: "astronomer.charlie-command-catalog/v1";
  version: number;
  commands: CharlieCommandDescriptor[];
};

export type CharlieCommandRequest = {
  id: string;
  version: string;
  arguments: Record<string, string>;
};

export async function getCharlieCommands(): Promise<CharlieCommandCatalog> {
  const { data } = await api.get("/charlie/commands/");
  return (data?.data ?? data) as CharlieCommandCatalog;
}

export async function getCharlieActiveThread(): Promise<CharlieActiveThread> {
  const { data } = await api.get("/charlie/threads/active/");
  const value = data?.data ?? data;
  return {
    thread: value?.thread ?? null,
    messageable: Boolean(value?.messageable),
    needs_continue: Boolean(value?.needs_continue),
    session_ids: Array.isArray(value?.session_ids) ? value.session_ids : [],
    current_session: value?.current_session
      ? mapCharlieSession(value.current_session)
      : null,
  };
}

export async function newCharlieChat(): Promise<CharlieActiveThread> {
  const { data } = await api.post("/charlie/threads/new/", {});
  const value = data?.data ?? data;
  return {
    thread: value?.thread ?? null,
    messageable: Boolean(value?.messageable),
    needs_continue: Boolean(value?.needs_continue),
    session_ids: Array.isArray(value?.session_ids) ? value.session_ids : [],
    current_session: value?.current_session
      ? mapCharlieSession(value.current_session)
      : null,
  };
}

export async function sendCharlieThreadMessage(
  message: string,
  options?: {
    trigger?: string;
    currentUiContext?: string;
    resources?: CharlieResource[];
    command?: CharlieCommandRequest;
  },
): Promise<CharlieActiveThread & { receipt?: CharlieTurnReceipt }> {
  const { data } = await api.post("/charlie/threads/messages/", {
    client_message_id: crypto.randomUUID(),
    message,
    trigger: options?.trigger,
    current_ui_context: options?.currentUiContext,
    resources: options?.resources?.map((r) => ({
      type: r.type,
      id: r.id,
      required_verb: r.requiredVerb,
    })),
    command: options?.command,
  });
  const value = data?.data ?? data;
  const rawReceipt = value?.receipt;
  const receiptSessionId = rawReceipt?.sessionId ?? rawReceipt?.session_id;
  const receiptTurnId = rawReceipt?.turnId ?? rawReceipt?.turn_id;
  const receiptAcceptedAt = rawReceipt?.acceptedAt ?? rawReceipt?.accepted_at;
  const receipt =
    rawReceipt &&
    typeof receiptSessionId === "string" &&
    receiptSessionId &&
    typeof receiptTurnId === "string" &&
    receiptTurnId
      ? {
          sessionId: receiptSessionId,
          turnId: receiptTurnId,
          ...(typeof receiptAcceptedAt === "string"
            ? { acceptedAt: receiptAcceptedAt }
            : {}),
        }
      : undefined;
  return {
    thread: value?.thread ?? null,
    messageable: Boolean(value?.messageable),
    needs_continue: Boolean(value?.needs_continue),
    session_ids: Array.isArray(value?.session_ids) ? value.session_ids : [],
    current_session: value?.current_session
      ? mapCharlieSession(value.current_session)
      : null,
    receipt,
  };
}

export async function getCharlieThreadHistory(
  threadId: string,
): Promise<CharlieMessage[]> {
  const { data } = await api.get(
    `/charlie/threads/${encodeURIComponent(threadId)}/history/`,
  );
  const value = data.items ?? data.data?.items ?? data.data ?? data;
  if (!Array.isArray(value)) return [];
  return (value as CharlieHistoryItemWire[]).map((item) => {
    const citations = (Array.isArray(item.citations) ? item.citations : [])
      .slice(0, 16)
      .flatMap((citation) => {
        const id = typeof citation.id === "string" ? citation.id : "";
        const title =
          typeof citation.title === "string" ? citation.title.trim() : "";
        const source =
          typeof citation.source === "string" ? citation.source.trim() : "";
        if (!id || !title || !source) return [];
        return [
          {
            id: id.slice(0, 128),
            title: title.slice(0, 1024),
            source: source.slice(0, 2048),
          },
        ];
      });
    return {
      id: item.itemId ?? item.item_id ?? "",
      role:
        item.kind === "user_message"
          ? "user"
          : item.kind === "assistant_message"
            ? "assistant"
            : "system",
      content: item.redactedContent ?? item.redacted_content ?? "",
      ...(citations.length ? { citations } : {}),
      createdAt: item.createdAt ?? item.created_at,
    };
  });
}

export async function listCharlieThreads(): Promise<
  Array<{ id: string; title: string; state: string; updated_at?: string }>
> {
  const { data } = await api.get("/charlie/threads/");
  const value = data.threads ?? data.data?.threads ?? data.data ?? data;
  if (!Array.isArray(value)) return [];
  return value.map((row: Record<string, unknown>) => ({
    id: String(row.id ?? ""),
    title: String(row.title ?? ""),
    state: String(row.state ?? ""),
    updated_at:
      typeof row.updated_at === "string"
        ? row.updated_at
        : typeof row.updatedAt === "string"
          ? row.updatedAt
          : undefined,
  }));
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

const charlieTerminalEventTypes = new Set<string>([
  "turn.completed",
  "turn.failed",
  "turn.aborted",
  "charlie.error",
]);

// EventSource keeps its last confirmed event ID and sends Last-Event-ID on an
// automatic reconnect. Astronomer also persists the cursor after flushing each
// event, so a new browser/server can resume without storing conversation data.
//
// Browser EventSource retries aggressively after a 429/network blip. That can
// thrash the product-local concurrency gate and leave the UI stuck on
// "reconnecting" even after the stream is healthy again. Own reconnect with
// backoff, clear unavailable on open, and stop retries when unsubscribed.
export function subscribeCharlieSessionEvents(
  id: string,
  onEvent: (event: MessageEvent<string>) => void,
  onError: () => void,
  onOpen?: () => void,
): () => void {
  const base = API_BASE.replace(/\/$/, "");
  const url = `${base}/charlie/sessions/${encodeURIComponent(id)}/events/`;
  let closed = false;
  let source: EventSource | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let attempt = 0;
  let terminalSeen = false;
  const listener = (event: Event) => {
    onEvent(event as MessageEvent<string>);
    if (charlieTerminalEventTypes.has(event.type)) terminalSeen = true;
  };

  const clearRetry = () => {
    if (retryTimer !== undefined) {
      clearTimeout(retryTimer);
      retryTimer = undefined;
    }
  };

  const detach = (es: EventSource) => {
    for (const type of charlieSessionEventTypes) {
      es.removeEventListener(type, listener);
    }
    es.onopen = null;
    es.onerror = null;
    es.close();
  };

  const connect = () => {
    if (closed) return;
    clearRetry();
    const es = new EventSource(url, { withCredentials: true });
    source = es;
    for (const type of charlieSessionEventTypes) {
      es.addEventListener(type, listener);
    }
    es.onopen = () => {
      attempt = 0;
      onOpen?.();
    };
    es.onerror = () => {
      // Charlie closes a completed turn stream after the terminal event. That
      // is a successful end, not a connectivity failure and must not produce a
      // false reconnect warning. A later turn creates a fresh subscription.
      if (terminalSeen) {
        detach(es);
        if (source === es) source = null;
        return;
      }
      onError();
      if (closed) return;
      // Stop the browser's immediate reconnect loop; we reopen with backoff.
      detach(es);
      if (source === es) source = null;
      attempt += 1;
      const delayMs = Math.min(30_000, 500 * 2 ** Math.min(attempt, 5));
      retryTimer = setTimeout(connect, delayMs);
    };
  };

  connect();
  return () => {
    closed = true;
    clearRetry();
    if (source) {
      detach(source);
      source = null;
    }
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
