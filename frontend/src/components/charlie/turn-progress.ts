export type CharlieTurnProgress = {
  stage:
    | "queued"
    | "planning"
    | "preparing_tool"
    | "running_tool"
    | "waiting_approval"
    | "analyzing"
    | "drafting"
    | "completed"
    | "failed"
    | "aborted";
  label: string;
  capability?: string;
  startedAt: number;
  lastEventAt: number;
  toolCallIds: string[];
  completedToolCallIds: string[];
  eventCount: number;
  seenEventIds: string[];
};

export type CharlieProgressEvent = {
  type: string;
  data: string;
  lastEventId?: string;
};

const MAX_TRACKED_IDS = 128;
const SAFE_CAPABILITY = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const SAFE_TOOL_CALL_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const SAFE_TURN_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;

export function initialCharlieTurnProgress(now = Date.now()): CharlieTurnProgress {
  return {
    stage: "queued",
    label: "Sending request to Charlie",
    startedAt: now,
    lastEventAt: now,
    toolCallIds: [],
    completedToolCallIds: [],
    eventCount: 0,
    seenEventIds: [],
  };
}

function boundedID(value: unknown, pattern: RegExp): string | undefined {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim();
  return pattern.test(normalized) ? normalized : undefined;
}

function appendUnique(values: string[], value?: string): string[] {
  if (!value || values.includes(value)) return values;
  return [...values, value].slice(-MAX_TRACKED_IDS);
}

function eventEnvelope(raw: string): Record<string, unknown> {
  try {
    const envelope = JSON.parse(raw) as unknown;
    return envelope && typeof envelope === "object" && !Array.isArray(envelope)
      ? (envelope as Record<string, unknown>)
      : {};
  } catch {
    return {};
  }
}

function eventData(raw: string): Record<string, unknown> {
  const data = eventEnvelope(raw).data;
  return data && typeof data === "object" && !Array.isArray(data)
    ? (data as Record<string, unknown>)
    : {};
}

/** Return only the bounded turn identity needed to correlate lifecycle events. */
export function charlieProgressEventTurnId(raw: string): string | undefined {
  const envelope = eventEnvelope(raw);
  return boundedID(envelope.turn_id, SAFE_TURN_ID);
}

/**
 * Reduce one authenticated Charlie lifecycle event into content-free UI state.
 * Tool arguments/results and response text are deliberately never retained.
 */
export function updateCharlieTurnProgress(
  current: CharlieTurnProgress,
  event: CharlieProgressEvent,
  now = Date.now(),
): CharlieTurnProgress {
  const eventID = boundedID(event.lastEventId, SAFE_TOOL_CALL_ID);
  if (eventID && current.seenEventIds.includes(eventID)) return current;

  const data = eventData(event.data);
  const capability = boundedID(data.capability, SAFE_CAPABILITY);
  const toolCallID = boundedID(data.tool_call_id, SAFE_TOOL_CALL_ID);
  const next: CharlieTurnProgress = {
    ...current,
    lastEventAt: now,
    eventCount: current.eventCount + 1,
    seenEventIds: appendUnique(current.seenEventIds, eventID),
  };

  switch (event.type) {
    case "turn.started":
      return { ...next, stage: "planning", label: "Planning the investigation" };
    case "tool.proposed":
      return {
        ...next,
        stage: "preparing_tool",
        label: capability ? `Preparing ${capability}` : "Preparing a diagnostic tool",
        capability,
        toolCallIds: appendUnique(current.toolCallIds, toolCallID),
      };
    case "tool.running":
      return {
        ...next,
        stage: "running_tool",
        label: capability ? `Calling ${capability}` : "Calling a diagnostic tool",
        capability,
        toolCallIds: appendUnique(current.toolCallIds, toolCallID),
      };
    case "tool.succeeded":
      return {
        ...next,
        stage: "analyzing",
        label: capability ? `${capability} completed · Analyzing results` : "Tool completed · Analyzing results",
        capability,
        toolCallIds: appendUnique(current.toolCallIds, toolCallID),
        completedToolCallIds: appendUnique(current.completedToolCallIds, toolCallID),
      };
    case "tool.failed":
      return {
        ...next,
        stage: "analyzing",
        label: capability ? `${capability} failed · Adjusting the investigation` : "Tool failed · Adjusting the investigation",
        capability,
        toolCallIds: appendUnique(current.toolCallIds, toolCallID),
        completedToolCallIds: appendUnique(current.completedToolCallIds, toolCallID),
      };
    case "permission.requested":
      return {
        ...next,
        stage: "waiting_approval",
        label: capability ? `Waiting for approval · ${capability}` : "Waiting for approval",
        capability,
        toolCallIds: appendUnique(current.toolCallIds, toolCallID),
      };
    case "permission.responded":
      return { ...next, stage: "analyzing", label: "Approval decision received · Continuing" };
    case "text.delta":
      return { ...next, stage: "drafting", label: "Drafting the response" };
    case "turn.completed":
      return { ...next, stage: "completed", label: "Response complete" };
    case "turn.failed":
    case "charlie.error":
      return { ...next, stage: "failed", label: "Charlie could not complete the response" };
    case "turn.aborted":
      return { ...next, stage: "aborted", label: "Turn aborted" };
    default:
      return next;
  }
}
