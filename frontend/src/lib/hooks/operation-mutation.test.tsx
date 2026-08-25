import type { PropsWithChildren } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import { useOperationMutation } from "./operation-mutation";

function wrapper({ children }: PropsWithChildren) {
  const client = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useOperationMutation", () => {
  it("cancels polling on unmount", async () => {
    let submittedSignal: AbortSignal | undefined;
    const submit = vi.fn(
      async (_: string, context: { signal: AbortSignal }) => {
        submittedSignal = context.signal;
        return { id: "op-1", status: "pending" };
      },
    );
    const read = vi.fn(async () => ({ id: "op-1", status: "running" }));
    const { result, unmount } = renderHook(
      () => useOperationMutation({ keyPrefix: "test", submit, read }),
      { wrapper },
    );

    let pending!: Promise<unknown>;
    act(() => {
      pending = result.current.mutateAsync("value");
    });
    const assertion = expect(pending).rejects.toMatchObject({
      name: "AbortError",
    });
    await waitFor(() => expect(submit).toHaveBeenCalledOnce());
    unmount();

    expect(submittedSignal?.aborted).toBe(true);
    await assertion;
    expect(read).not.toHaveBeenCalled();
  });
});
