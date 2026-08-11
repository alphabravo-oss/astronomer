import { describe, expect, it } from "vitest";
import {
  initialCharlieTurnProgress,
  updateCharlieTurnProgress,
} from "../turn-progress";

function event(type: string, data: Record<string, unknown>, lastEventId: string) {
  return {
    type,
    lastEventId,
    data: JSON.stringify({
      id: lastEventId,
      type,
      data,
    }),
  };
}

describe("Charlie turn progress", () => {
  it("shows real tool lifecycle and live updates without retaining content", () => {
    let progress = initialCharlieTurnProgress(1000);
    progress = updateCharlieTurnProgress(progress, event("turn.started", {}, "1"), 1100);
    expect(progress.label).toBe("Planning the investigation");

    progress = updateCharlieTurnProgress(progress, event("tool.running", {
      capability: "astronomer.queue.tasks",
      tool_call_id: "call-1",
      input: { queue: "default", credential: "SENTINEL" },
    }, "2"), 1200);
    expect(progress.label).toBe("Calling astronomer.queue.tasks");
    expect(progress.toolCallIds).toEqual(["call-1"]);
    expect(JSON.stringify(progress)).not.toContain("SENTINEL");

    progress = updateCharlieTurnProgress(progress, event("tool.succeeded", {
      capability: "astronomer.queue.tasks",
      tool_call_id: "call-1",
      result: { secret: "SENTINEL" },
    }, "3"), 1300);
    expect(progress.completedToolCallIds).toEqual(["call-1"]);
    expect(JSON.stringify(progress)).not.toContain("SENTINEL");

    progress = updateCharlieTurnProgress(progress, event("text.delta", {
      text: "private response content",
    }, "4"), 1400);
    expect(progress.label).toBe("Drafting the response");
    expect(progress.eventCount).toBe(4);
    expect(JSON.stringify(progress)).not.toContain("private response content");
  });

  it("ignores replayed event IDs and unsafe capability labels", () => {
    const initial = initialCharlieTurnProgress(1000);
    const once = updateCharlieTurnProgress(initial, event("tool.running", {
      capability: "<script>SENTINEL</script>",
      tool_call_id: "call-1",
    }, "2"), 1200);
    expect(once.label).toBe("Calling a diagnostic tool");
    const replay = updateCharlieTurnProgress(once, event("text.delta", { text: "duplicate" }, "2"), 1300);
    expect(replay).toBe(once);
    expect(replay.eventCount).toBe(1);
  });
});
