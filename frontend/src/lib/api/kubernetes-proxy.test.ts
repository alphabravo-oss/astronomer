import { beforeEach, describe, expect, it, vi } from "vitest";

import { postClustersByIdGenerateDirectKubeconfig } from "@/lib/api/generated/client";
import { downloadDirectKubeconfig } from "@/lib/api/kubernetes-proxy";

vi.mock("@/lib/api/generated/client", () => ({
  postClustersByIdGenerateDirectKubeconfig: vi.fn(),
}));

vi.mock("@/lib/api/transport", () => ({
  default: { post: vi.fn(), request: vi.fn() },
}));

describe("downloadDirectKubeconfig", () => {
  beforeEach(() => vi.clearAllMocks());

  it("uses the generated fixed-path operation and preserves the Blob download contract", async () => {
    vi.mocked(postClustersByIdGenerateDirectKubeconfig).mockResolvedValue(
      "apiVersion: v1\nkind: Config\n",
    );

    const result = await downloadDirectKubeconfig("cluster-1");

    expect(postClustersByIdGenerateDirectKubeconfig).toHaveBeenCalledWith({
      path: { id: "cluster-1" },
    });
    expect(result).toBeInstanceOf(Blob);
    expect(result.type).toBe("application/x-yaml;charset=utf-8");
    expect(result.size).toBeGreaterThan(0);
  });

  it("propagates the generated API error so the download hook can show it", async () => {
    const error = new Error("mandatory audit unavailable");
    vi.mocked(postClustersByIdGenerateDirectKubeconfig).mockRejectedValue(
      error,
    );

    await expect(downloadDirectKubeconfig("cluster-1")).rejects.toBe(error);
  });
});
