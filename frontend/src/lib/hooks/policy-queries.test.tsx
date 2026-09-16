import type { PropsWithChildren } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useComplianceBaselines, useComplianceBaselineDiff, useNetworkPolicyApplications, useNetworkPolicyTemplates } from "./policy-queries";

const api = vi.hoisted(() => ({
  listNetworkPolicyApplications: vi.fn(),
  listNetworkPolicyTemplates: vi.fn(),
  listComplianceBaselines: vi.fn(),
  listComplianceBaselineApplications: vi.fn(),
  getActiveComplianceBaseline: vi.fn(),
  getComplianceBaselineDiff: vi.fn(),
}));
vi.mock("@/lib/api/settings", () => api);

function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { wrapper };
}

beforeEach(() => vi.resetAllMocks());

it("cancels the previous cluster request and never paints its late response under the new cluster", async () => {
  let resolveA!: (value: unknown[]) => void;
  api.listNetworkPolicyApplications.mockImplementation((id: string) => id === "a"
    ? new Promise((resolve) => { resolveA = resolve; })
    : Promise.resolve([{ id: "policy-b" }]));
  const { result, rerender } = renderHook(({ id }) => useNetworkPolicyApplications(id), { initialProps: { id: "a" }, ...setup() });
  await waitFor(() => expect(api.listNetworkPolicyApplications).toHaveBeenCalledTimes(1));
  const signal = api.listNetworkPolicyApplications.mock.calls[0][1] as AbortSignal;
  rerender({ id: "b" });
  await waitFor(() => expect(result.current.data).toEqual([{ id: "policy-b" }]));
  expect(signal.aborted).toBe(true);
  await act(async () => resolveA([{ id: "policy-a" }]));
  expect(result.current.data).toEqual([{ id: "policy-b" }]);
});

it("aborts template reads when the consuming settings page unmounts", async () => {
  api.listNetworkPolicyTemplates.mockImplementation(() => new Promise(() => {}));
  const { unmount } = renderHook(useNetworkPolicyTemplates, setup());
  await waitFor(() => expect(api.listNetworkPolicyTemplates).toHaveBeenCalledTimes(1));
  const signal = api.listNetworkPolicyTemplates.mock.calls[0][0] as AbortSignal;
  unmount();
  expect(signal.aborted).toBe(true);
});

it("surfaces history failures instead of claiming no compliance baseline was applied", async () => {
  api.listComplianceBaselines.mockResolvedValue([{ slug: "pci", active: true }]);
  api.listComplianceBaselineApplications.mockRejectedValue(new Error("history unavailable"));
  api.getActiveComplianceBaseline.mockResolvedValue({ active: { baselineSlug: "pci" } });
  const { result } = renderHook(useComplianceBaselines, setup());
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(result.current.data).toBeUndefined();
});

it("clears stale active flags when the server reports no active baseline", async () => {
  api.listComplianceBaselines.mockResolvedValue([{ slug: "pci", active: true }]);
  api.listComplianceBaselineApplications.mockResolvedValue([]);
  api.getActiveComplianceBaseline.mockResolvedValue({ active: null });
  const { result } = renderHook(useComplianceBaselines, setup());
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(result.current.data?.baselines).toEqual([{ slug: "pci", active: false }]);
});

it("cancels the diff request when the preview closes", async () => {
  api.getComplianceBaselineDiff.mockImplementation(() => new Promise(() => {}));
  const { unmount } = renderHook(() => useComplianceBaselineDiff("pci"), setup());
  await waitFor(() => expect(api.getComplianceBaselineDiff).toHaveBeenCalledTimes(1));
  const { signal } = api.getComplianceBaselineDiff.mock.calls[0][1] as { signal: AbortSignal };
  unmount();
  expect(signal.aborted).toBe(true);
});
