import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import NativeRulesTab from "./-native-rules-tab";
import { Input } from "@/components/ui/input";
import {
  getNativeRulePage,
  createNativeRule,
  deleteNativeRule,
} from "@/lib/api/native-rbac";
vi.mock("@/components/clusters/remote-cluster-picker", () => ({
  RemoteClusterPicker: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (value: string) => void;
  }) => (
    <Input
      aria-label="Cluster UUID"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
}));
const rights = vi.hoisted(() => ({ read: true, create: false, delete: false }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: (_resource: string, verb: keyof typeof rights) => ({
    allowed: rights[verb],
  }),
}));
vi.mock("@/lib/api/native-rbac", () => ({
  getNativeRulePage: vi.fn(),
  createNativeRule: vi.fn(),
  deleteNativeRule: vi.fn(),
}));
const renderTab = () =>
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <NativeRulesTab />
    </QueryClientProvider>,
  );
describe("native RBAC permission and availability gates", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    rights.read = true;
    rights.create = false;
    rights.delete = false;
  });
  it("does not enumerate grants without read permission", () => {
    rights.read = false;
    renderTab();
    expect(screen.getByText("rbac:read")).toBeInTheDocument();
    expect(getNativeRulePage).not.toHaveBeenCalled();
  });
  it("keeps create disabled for a read-only actor on an empty page", async () => {
    vi.mocked(getNativeRulePage).mockResolvedValue({
      data: [],
      pagination: { limit: 25, offset: 0, has_more: false, next_offset: null },
    });
    renderTab();
    await screen.findByText("No native grants");
    expect(
      screen.queryByRole("button", { name: /^Sort by/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create native grant" }),
    ).toBeDisabled();
    expect(createNativeRule).not.toHaveBeenCalled();
    expect(getNativeRulePage).toHaveBeenCalledWith(
      { limit: 25, offset: 0 },
      expect.any(AbortSignal),
    );
  });
  it("explains a disabled native RBAC endpoint instead of offering a broken form", async () => {
    rights.create = true;
    vi.mocked(getNativeRulePage).mockRejectedValue({ status: 404 });
    renderTab();
    await screen.findByText("Native RBAC is unavailable");
    expect(
      screen.getByRole("button", { name: "Create native grant" }),
    ).toBeDisabled();
  });
  it("creates an exact scoped grant only after review and typed confirmation", async () => {
    rights.create = true;
    vi.mocked(getNativeRulePage).mockResolvedValue({
      data: [],
      pagination: { limit: 25, offset: 0, has_more: false, next_offset: null },
    });
    const userId = "00000000-0000-4000-8000-000000000001";
    const clusterId = "00000000-0000-4000-8000-000000000002";
    vi.mocked(createNativeRule).mockResolvedValue({
      id: "grant",
      userId,
      clusterId,
      namespace: "app",
      apiGroup: "",
      resource: "configmaps",
      verbs: ["read"],
      createdAt: "2026-09-22T00:00:00Z",
    });
    renderTab();
    await screen.findByText("No native grants");
    fireEvent.click(
      screen.getByRole("button", { name: "Create native grant" }),
    );
    fireEvent.change(screen.getByLabelText("User UUID"), {
      target: { value: userId },
    });
    fireEvent.change(screen.getByLabelText("Cluster UUID"), {
      target: { value: clusterId },
    });
    fireEvent.change(screen.getByLabelText(/^Namespace/), {
      target: { value: "app" },
    });
    fireEvent.change(screen.getByLabelText("Plural resource"), {
      target: { value: "configmaps" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Review grant" }));
    expect(createNativeRule).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Grant access" })).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("grant access"), {
      target: { value: "grant access" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Grant access" }));
    await waitFor(() =>
      expect(createNativeRule).toHaveBeenCalledWith({
        userId,
        clusterId,
        namespace: "app",
        apiGroup: "",
        resource: "configmaps",
        verbs: ["read"],
      }),
    );
  });
  it("requires typed confirmation before removing a grant", async () => {
    rights.delete = true;
    vi.mocked(getNativeRulePage).mockResolvedValue({
      data: [
        {
          id: "grant",
          userId: "user",
          namespace: "app",
          apiGroup: "",
          resource: "configmaps",
          verbs: ["read"],
          createdAt: "2026-09-22T00:00:00Z",
        },
      ],
      pagination: { limit: 25, offset: 0, has_more: false, next_offset: null },
    });
    renderTab();
    fireEvent.click(
      await screen.findByRole("button", { name: "Remove grant" }),
    );
    expect(deleteNativeRule).not.toHaveBeenCalled();
    fireEvent.change(screen.getByPlaceholderText("remove grant"), {
      target: { value: "remove grant" },
    });
    fireEvent.click(
      screen.getAllByRole("button", { name: "Remove grant" }).at(-1)!,
    );
    await waitFor(() => expect(deleteNativeRule).toHaveBeenCalledWith("grant"));
  });
});
