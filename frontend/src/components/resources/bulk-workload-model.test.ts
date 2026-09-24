import { beforeEach, describe, expect, it, vi } from "vitest";
import { runBulkWorkloads } from "./bulk-workload-model";
import {
  getWorkloadOperation,
  restartWorkload,
  scaleWorkload,
} from "@/lib/api/workloads";
vi.mock("@/lib/api/workloads", () => ({
  getWorkloadOperation: vi.fn(),
  restartWorkload: vi.fn(),
  scaleWorkload: vi.fn(),
}));
const targets = ["one", "two"].map((name) => ({
  kind: "Deployment",
  name,
  namespace: "app",
}));
const options = () => ({
  clusterId: "cluster",
  targets,
  action: "restart" as const,
  replicas: 1,
  signal: new AbortController().signal,
  allowed: () => true,
});
describe("bounded sequential workload mutations", () => {
  beforeEach(() => vi.resetAllMocks());
  it("does not submit the next target before the first completes", async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(restartWorkload)
        .mockResolvedValueOnce({ id: "op1", status: "queued" })
        .mockResolvedValueOnce({ id: "op2", status: "succeeded" });
      vi.mocked(getWorkloadOperation).mockResolvedValueOnce({
        id: "op1",
        status: "succeeded",
      });
      const promise = runBulkWorkloads(options());
      await Promise.resolve();
      expect(restartWorkload).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(500);
      expect((await promise).map((row) => row.status)).toEqual([
        "succeeded",
        "succeeded",
      ]);
      expect(getWorkloadOperation).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
  it("reports terminal partial failure and proceeds independently", async () => {
    vi.mocked(restartWorkload)
      .mockResolvedValueOnce({ id: "one", status: "partial" })
      .mockResolvedValueOnce({ id: "two", status: "succeeded" });
    expect(
      (await runBulkWorkloads(options())).map((row) => row.status),
    ).toEqual(["failed", "succeeded"]);
  });
  it("stops after a lost response, without reporting a false failure or retrying", async () => {
    vi.mocked(restartWorkload).mockRejectedValueOnce(
      new Error("Network unavailable"),
    );
    expect(
      (await runBulkWorkloads(options())).map((row) => row.status),
    ).toEqual(["unconfirmed", "not-started"]);
    expect(restartWorkload).toHaveBeenCalledTimes(1);
  });
  it("rechecks each permission and never submits denied targets", async () => {
    const result = await runBulkWorkloads({
      ...options(),
      allowed: () => false,
    });
    expect(result.every((row) => row.status === "not-started")).toBe(true);
    expect(restartWorkload).not.toHaveBeenCalled();
  });
  it("does not send requests after cancellation", async () => {
    const controller = new AbortController();
    controller.abort();
    const result = await runBulkWorkloads({
      ...options(),
      signal: controller.signal,
    });
    expect(result.every((row) => row.status === "not-started")).toBe(true);
    expect(restartWorkload).not.toHaveBeenCalled();
  });
  it("rejects oversized batches and invalid replica counts before any write", async () => {
    await expect(
      runBulkWorkloads({ ...options(), targets: Array(51).fill(targets[0]) }),
    ).rejects.toThrow("1–50");
    await expect(
      runBulkWorkloads({ ...options(), action: "scale", replicas: -1 }),
    ).rejects.toThrow("non-negative integer");
    expect(scaleWorkload).not.toHaveBeenCalled();
    expect(restartWorkload).not.toHaveBeenCalled();
  });
});
