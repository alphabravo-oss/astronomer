import { act, renderHook } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { subscribeCharlieSessionEvents } from "@/lib/api/charlie";
import { useCharlieSessionState } from "../charlie-session-state";
import { useCharlieSessionStream } from "../use-charlie-session-stream";

vi.mock("@/lib/api/charlie", () => ({
  subscribeCharlieSessionEvents: vi.fn(),
}));

function renderStream(client: QueryClient) {
  return renderHook(() => {
    const state = useCharlieSessionState();
    useCharlieSessionStream({
      sessionId: "session",
      threadId: "thread",
      queryClient: client,
      generation: state.streamGeneration,
      awaitingReplyRef: state.awaitingReplyRef,
      activeTurnIdRef: state.activeTurnIdRef,
      setAwaitingReply: state.setAwaitingReply,
      setStreamUnavailable: state.setStreamUnavailable,
      setTurnFailed: state.setTurnFailed,
      setTurnProgress: state.setTurnProgress,
    });
    return state;
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers();
});
afterEach(() => vi.useRealTimers());

describe("Charlie session stream lifecycle", () => {
  it("ignores another turn's terminal frame and finishes only the active turn", () => {
    const stop = vi.fn();
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(stop);
    const { result, unmount } = renderStream(new QueryClient());
    act(() => {
      result.current.awaitingReplyRef.current = true;
      result.current.activeTurnIdRef.current = "current-turn";
      result.current.setAwaitingReply(true);
    });
    const receive = vi.mocked(subscribeCharlieSessionEvents).mock.calls[0][1];
    act(() =>
      receive(
        new MessageEvent("turn.failed", {
          data: JSON.stringify({ turn_id: "prior-turn" }),
        }),
      ),
    );
    expect(result.current.awaitingReply).toBe(true);
    expect(result.current.turnFailed).toBe(false);
    act(() =>
      receive(
        new MessageEvent("turn.failed", {
          data: JSON.stringify({ turn_id: "current-turn" }),
        }),
      ),
    );
    expect(result.current.awaitingReply).toBe(false);
    expect(result.current.turnFailed).toBe(true);
    unmount();
    expect(stop).toHaveBeenCalledOnce();
  });

  it("cancels its batched history refresh and subscription when hidden", () => {
    const client = new QueryClient();
    const invalidate = vi
      .spyOn(client, "invalidateQueries")
      .mockResolvedValue();
    const stop = vi.fn();
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(stop);
    const { unmount } = renderStream(client);
    const receive = vi.mocked(subscribeCharlieSessionEvents).mock.calls[0][1];
    act(() => receive(new MessageEvent("tool.running", { data: "{}" })));
    unmount();
    act(() => vi.advanceTimersByTime(1000));
    expect(stop).toHaveBeenCalledOnce();
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("replaces the subscription on a new send generation", () => {
    const stop = vi.fn();
    vi.mocked(subscribeCharlieSessionEvents).mockReturnValue(stop);
    const { result, unmount } = renderStream(new QueryClient());
    act(() => result.current.setStreamGeneration((value) => value + 1));
    expect(subscribeCharlieSessionEvents).toHaveBeenCalledTimes(2);
    expect(stop).toHaveBeenCalledOnce();
    unmount();
    expect(stop).toHaveBeenCalledTimes(2);
  });
});
