import { act, renderHook } from "@testing-library/react";
import { useClusterToolActions } from "@/components/clusters/use-cluster-tool-actions";
import type { ClusterTool, ClusterToolStatus } from "@/types";

const mocks = vi.hoisted(() => ({
  install: vi.fn(),
  uninstall: vi.fn(),
  adopt: vi.fn(),
  recover: vi.fn(),
  denied: vi.fn(),
  grants: { create: true, delete: true, update: true },
}));
vi.mock("@/lib/hooks/tools", () => ({
  useInstallTool: () => ({ mutate: mocks.install, isPending: false }),
  useUninstallTool: () => ({ mutate: mocks.uninstall, isPending: false }),
  useAdoptTool: () => ({ mutate: mocks.adopt }),
  useRecoverTool: () => ({ mutate: mocks.recover, isPending: false }),
}));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: (
    _resource: string,
    verb: keyof typeof mocks.grants,
  ) => ({ allowed: mocks.grants[verb], verb }),
  toastPermissionDenied: mocks.denied,
  permissionDeniedReason: ({ verb }: { verb: string }) => `${verb} denied`,
}));

const tool: ClusterTool = {
  id: "tool",
  slug: "istio",
  name: "Istio",
  description: "Service mesh",
  icon: "network",
  category: "mesh",
  charts: [],
  versionConstraint: "1.31.0",
  defaultNamespace: "istio-system",
  isBuiltin: true,
  isEnabled: true,
  presets: {},
  serviceName: "",
  servicePort: null,
  servicePath: "",
  subServices: [],
  createdAt: "2026-09-10",
  updatedAt: "2026-09-10",
};
const status: ClusterToolStatus = {
  slug: "istio",
  name: "Istio",
  status: "failed",
  releaseName: "istiod",
  namespace: "istio-system",
  presetUsed: "production",
  error: "failed",
  operation: {
    id: "original-operation",
    targetType: "tool_installation",
    targetKey: "cluster:istio",
    operationType: "install",
    status: "failed",
    attemptCount: 1,
    errorMessage: "failed",
    createdAt: "2026-09-10",
    updatedAt: "2026-09-10",
  },
};
const props = {
  clusterId: "cluster",
  clusterEnvironment: "production",
  tools: [tool],
  statuses: [status],
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.grants = { create: true, delete: true, update: true };
});

describe("useClusterToolActions", () => {
  it("hands a confirmed install to the operation drawer with the chosen values and preset", () => {
    const { result } = renderHook(() => useClusterToolActions(props));
    act(() => result.current.cardProps(tool).onInstall(tool.slug));
    expect(result.current.installDialog?.preset).toBe("production");
    act(() =>
      result.current.installDialog?.onConfirm(
        "istiod:\n  replicaCount: 2",
        "default",
      ),
    );
    expect(mocks.install.mock.calls[0][0]).toEqual({
      slug: "istio",
      cluster_id: "cluster",
      preset: "default",
      values_override: "istiod:\n  replicaCount: 2",
    });
    act(() =>
      mocks.install.mock.calls[0][1].onSuccess({ id: "new-operation" }),
    );
    expect(result.current.installDialog).toBeNull();
    expect(result.current.progress).toMatchObject({
      operationId: "new-operation",
      toolName: "Istio",
    });
    act(() => result.current.progress?.onClose());
    expect(result.current.progress).toBeNull();
  });

  it("rechecks permission when confirming an already-open dialog", () => {
    const { result, rerender } = renderHook(() => useClusterToolActions(props));
    act(() => result.current.cardProps(tool).onInstall(tool.slug));
    mocks.grants.create = false;
    rerender();
    act(() => result.current.installDialog?.onConfirm(undefined, "production"));
    expect(mocks.install).not.toHaveBeenCalled();
    expect(mocks.denied).toHaveBeenCalledOnce();
    expect(result.current.cardProps(tool).installDisabledReason).toBe(
      "create denied",
    );
  });

  it("retries the original operation directly but requires confirmation for rollback", () => {
    const { result } = renderHook(() => useClusterToolActions(props));
    act(() => result.current.cardProps(tool).onRecover?.(tool.slug, "retry"));
    expect(mocks.recover.mock.calls[0][0]).toEqual({
      slug: "istio",
      cluster_id: "cluster",
      operationId: "original-operation",
      action: "retry",
    });
    act(() =>
      result.current.cardProps(tool).onRecover?.(tool.slug, "rollback"),
    );
    expect(mocks.recover).toHaveBeenCalledTimes(1);
    expect(result.current.confirmation?.title).toBe("Roll back Istio");
    act(() => {
      void result.current.confirmation?.onConfirm();
    });
    expect(mocks.recover.mock.calls[1][0]).toMatchObject({
      action: "rollback",
      operationId: "original-operation",
    });
    act(() =>
      mocks.recover.mock.calls[1][1].onSuccess({ id: "rollback-operation" }),
    );
    expect(result.current.confirmation).toBeNull();
    expect(result.current.progress?.operationId).toBe("rollback-operation");
  });

  it("confirms uninstall and hands its returned operation to progress", () => {
    const { result } = renderHook(() => useClusterToolActions(props));
    act(() => result.current.cardProps(tool).onUninstall(tool.slug));
    expect(mocks.uninstall).not.toHaveBeenCalled();
    expect(result.current.confirmation?.description).toContain("Istio");
    act(() => {
      void result.current.confirmation?.onConfirm();
    });
    expect(mocks.uninstall.mock.calls[0][0]).toEqual({
      slug: "istio",
      cluster_id: "cluster",
    });
    act(() =>
      mocks.uninstall.mock.calls[0][1].onSuccess({ id: "uninstall-operation" }),
    );
    expect(result.current.progress?.operationId).toBe("uninstall-operation");
  });

  it("denies recovery before opening a dialog and preserves adoption scope", () => {
    mocks.grants.update = false;
    const { result } = renderHook(() => useClusterToolActions(props));
    act(() =>
      result.current.cardProps(tool).onRecover?.(tool.slug, "rollback"),
    );
    expect(result.current.confirmation).toBeNull();
    expect(mocks.recover).not.toHaveBeenCalled();
    act(() => result.current.cardProps(tool).onAdopt(tool.slug, "istio"));
    expect(mocks.adopt).toHaveBeenCalledWith({
      slug: "istio",
      cluster_id: "cluster",
      release_name: "istio",
    });
    expect(result.current.progress).toBeNull();
  });
});
