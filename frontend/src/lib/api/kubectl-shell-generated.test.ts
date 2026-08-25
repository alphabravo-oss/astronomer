import { beforeEach, describe, expect, it, vi } from "vitest";

const generated = vi.hoisted(() => ({
  getClustersByClusterIdShellSessionsById: vi.fn(),
  getClustersByClusterIdShellSessionsByIdCommands: vi.fn(),
  getClustersByIdShellSessions: vi.fn(),
  postClustersByIdShellSessions: vi.fn(),
  postClustersByIdShellSessionsBySessionIdClose: vi.fn(),
}));

vi.mock("@/lib/api/generated/client", () => generated);

import {
  closeShellSession,
  listShellSessionCommands,
  openShellSession,
} from "./kubectl-shell";

const sessionWire = {
  id: "00000000-0000-4000-8000-000000000001",
  cluster_id: "00000000-0000-4000-8000-000000000002",
  user_id: "00000000-0000-4000-8000-000000000003",
  status: "active" as const,
  pod_name: "astronomer-shell-1",
  pod_namespace: "kube-system",
  container: "shell",
  started_at: "2026-08-24T00:00:00Z",
  last_input_at: "2026-08-24T00:01:00Z",
  expires_at: "2026-08-24T00:31:00Z",
  idle_timeout_seconds: 1800,
  command_count: 2,
};

describe("generated kubectl shell API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the exact session wire shape and forwards cancellation", async () => {
    const signal = new AbortController().signal;
    generated.postClustersByIdShellSessions.mockResolvedValue({
      data: sessionWire,
    });

    await expect(
      openShellSession(sessionWire.cluster_id, { signal }),
    ).resolves.toMatchObject({
      clusterId: sessionWire.cluster_id,
      userId: sessionWire.user_id,
      podName: sessionWire.pod_name,
      idleTimeoutSeconds: 1800,
      commandCount: 2,
    });
    expect(generated.postClustersByIdShellSessions).toHaveBeenCalledWith({
      path: { id: sessionWire.cluster_id },
      signal,
    });
  });

  it("maps recorded commands and exact pagination arguments", async () => {
    generated.getClustersByClusterIdShellSessionsByIdCommands.mockResolvedValue(
      {
        data: [
          {
            command_at: "2026-08-24T00:02:00Z",
            command_line: "kubectl get pods -A",
          },
        ],
        count: 1,
        next: null,
        previous: null,
      },
    );

    await expect(
      listShellSessionCommands(sessionWire.cluster_id, sessionWire.id, {
        limit: 50,
        offset: 10,
      }),
    ).resolves.toEqual([
      {
        commandAt: "2026-08-24T00:02:00Z",
        commandLine: "kubectl get pods -A",
      },
    ]);
    expect(
      generated.getClustersByClusterIdShellSessionsByIdCommands,
    ).toHaveBeenCalledWith({
      path: { cluster_id: sessionWire.cluster_id, id: sessionWire.id },
      query: { limit: 50, offset: 10 },
      signal: undefined,
    });
  });

  it("returns the synchronous close receipt instead of inventing void", async () => {
    generated.postClustersByIdShellSessionsBySessionIdClose.mockResolvedValue({
      data: { status: "closed" },
    });

    await expect(
      closeShellSession(sessionWire.cluster_id, sessionWire.id),
    ).resolves.toEqual({ status: "closed" });
  });
});
