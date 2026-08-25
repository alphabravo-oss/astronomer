import { describe, expect, it, vi } from "vitest";

import {
  OperationFailedError,
  OperationPartialError,
  createIdempotencyKey,
  pollOperation,
} from "./operation-polling";

describe("durable operation polling", () => {
  it("uses bounded backoff and resolves only after terminal success", async () => {
    vi.useFakeTimers();
    const read = vi
      .fn()
      .mockResolvedValueOnce({ id: "op-1", status: "running" })
      .mockResolvedValueOnce({ id: "op-1", status: "succeeded" });
    const result = pollOperation({ id: "op-1", status: "pending" }, read, {
      initialDelayMs: 10,
      maxDelayMs: 20,
    });
    await vi.runAllTimersAsync();
    await expect(result).resolves.toEqual({ id: "op-1", status: "succeeded" });
    expect(read).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });

  it("rejects terminal failures and cancels polling", async () => {
    const failure = pollOperation(
      { id: "op-2", status: "failed", errorMessage: "worker failed" },
      vi.fn(),
    );
    await expect(failure).rejects.toBeInstanceOf(OperationFailedError);

    vi.useFakeTimers();
    const controller = new AbortController();
    const cancelled = pollOperation(
      { id: "op-3", status: "pending" },
      vi.fn(),
      { signal: controller.signal },
    );
    controller.abort();
    await expect(cancelled).rejects.toMatchObject({ name: "AbortError" });
    vi.useRealTimers();
  });

  it("creates printable bounded idempotency keys", () => {
    const key = createIdempotencyKey("pod-delete");
    expect(key).toMatch(/^pod-delete:/);
    expect(key.length).toBeLessThanOrEqual(128);
  });

  it("keeps retrying nonterminal and reports blocked operations as partial", async () => {
    vi.useFakeTimers();
    const read = vi
      .fn()
      .mockResolvedValueOnce({ id: "op-4", status: "retrying" })
      .mockResolvedValueOnce({
        id: "op-4",
        status: "blocked",
        errorMessage: "pod/default/api:empty_dir",
      });
    const result = pollOperation({ id: "op-4", status: "pending" }, read, {
      initialDelayMs: 10,
      maxDelayMs: 20,
    });
    const assertion = expect(result).rejects.toBeInstanceOf(
      OperationPartialError,
    );
    await vi.runAllTimersAsync();
    await assertion;
    expect(read).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });

  it("stops after the configured polling bound", async () => {
    vi.useFakeTimers();
    const read = vi.fn().mockResolvedValue({ id: "op-5", status: "running" });
    const result = pollOperation({ id: "op-5", status: "pending" }, read, {
      maxAttempts: 2,
      maxDurationMs: 60_000,
      initialDelayMs: 10,
      maxDelayMs: 10,
    });
    const assertion = expect(result).rejects.toThrow("polling timed out");
    await vi.runAllTimersAsync();
    await assertion;
    expect(read).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });
});
