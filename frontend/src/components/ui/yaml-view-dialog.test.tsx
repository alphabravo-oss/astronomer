import type { MockedFunction } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import {
  classifyResourceApplyFailure,
  resourceTypeFromK8sPath,
  YamlPanel,
} from "./yaml-view-dialog";
import * as hooks from "@/lib/hooks";

// Mock the data hooks so we can drive `useK8sGetYaml`'s returned value.
vi.mock("@/lib/hooks", () => ({
  useK8sGetYaml: vi.fn(),
  useK8sApplyYaml: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useK8sDryRunYaml: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useResourceSchema: vi.fn(() => ({
    data: undefined,
    isLoading: false,
  })),
}));

// Replace the heavy editor with a plain textarea that surfaces value/onChange.
vi.mock("@/components/ui/yaml-editor", () => ({
  YamlEditor: ({
    value,
    onChange,
  }: {
    value: string;
    onChange?: (v: string) => void;
  }) => (
    <textarea
      data-testid="yaml-editor"
      value={value}
      readOnly={!onChange}
      onChange={(e) => onChange && onChange(e.target.value)}
    />
  ),
}));

const mockedGetYaml = hooks.useK8sGetYaml as MockedFunction<
  typeof hooks.useK8sGetYaml
>;
const mockedApplyYaml = hooks.useK8sApplyYaml as MockedFunction<
  typeof hooks.useK8sApplyYaml
>;

const permission = (allowed: boolean) => ({
  allowed,
  permission: "workloads:manage",
  scope: { type: "cluster" as const, id: "c1" },
  scopeLabel: "cluster c1",
  reason: allowed
    ? "Granted workloads:manage."
    : "Requires workloads:manage on cluster c1.",
  disabledReason: allowed
    ? undefined
    : "Requires workloads:manage on cluster c1.",
  grantedBy: [],
  requestAccessHint: "Ask a cluster administrator.",
});

function loadedYaml(refetch = vi.fn()) {
  mockedGetYaml.mockReturnValue({
    data: "name: v1",
    isLoading: false,
    error: null,
    refetch,
  } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);
  return refetch;
}

// Regression: a background refetch (window-focus refetch or a k8s.all cache
// invalidation from any mutation) must NOT overwrite the operator's in-progress
// edits while the editor is in edit mode.
describe("YamlPanel — edit-mode preservation", () => {
  afterEach(() => vi.clearAllMocks());

  it("keeps in-progress edits when the server YAML refetches during editing", () => {
    const refetch = vi.fn();
    mockedGetYaml.mockReturnValue({
      data: "name: v1",
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);

    const { getByText, getByTestId, rerender } = render(
      <YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />,
    );

    // Enter edit mode and type changes.
    fireEvent.click(getByText("Edit"));
    fireEvent.change(getByTestId("yaml-editor"), {
      target: { value: "name: my-edits" },
    });
    expect((getByTestId("yaml-editor") as HTMLTextAreaElement).value).toBe(
      "name: my-edits",
    );

    // Background refetch delivers a *different* server copy (changed
    // resourceVersion / managedFields timestamps in real life).
    mockedGetYaml.mockReturnValue({
      data: "name: v2-from-server",
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);
    rerender(
      <YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />,
    );

    // Edits survive — the editor was NOT re-seeded from the server copy.
    expect((getByTestId("yaml-editor") as HTMLTextAreaElement).value).toBe(
      "name: my-edits",
    );
  });

  it("does seed the editor from the server copy while in view mode", () => {
    const refetch = vi.fn();
    mockedGetYaml.mockReturnValue({
      data: "name: v1",
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);

    const { getByText, getByTestId, rerender } = render(
      <YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />,
    );
    // Stay in view mode; a refetch should reflect the fresh server copy once
    // the user switches to edit.
    mockedGetYaml.mockReturnValue({
      data: "name: v2-from-server",
      isLoading: false,
      error: null,
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);
    rerender(
      <YamlPanel clusterId="c1" k8sPath="api/v1/namespaces/default/pods/p" />,
    );

    fireEvent.click(getByText("Edit"));
    expect((getByTestId("yaml-editor") as HTMLTextAreaElement).value).toBe(
      "name: v2-from-server",
    );
  });
});

describe("resourceTypeFromK8sPath", () => {
  it("maps namespaced, cluster-scoped, and extension API paths", () => {
    expect(
      resourceTypeFromK8sPath(
        "apis/apps/v1/namespaces/default/deployments/web",
      ),
    ).toBe("deployments");
    expect(resourceTypeFromK8sPath("api/v1/namespaces/team-a")).toBe(
      "namespaces",
    );
    expect(
      resourceTypeFromK8sPath(
        "apis/gateway.networking.k8s.io/v1/namespaces/default/gateways/public",
      ),
    ).toBe("gateways");
    expect(resourceTypeFromK8sPath("api/v1/namespaces/default/pods/p")).toBe(
      undefined,
    );
  });
});

describe("YamlPanel — structured recovery", () => {
  afterEach(() => vi.clearAllMocks());

  it("classifies status/code metadata without parsing message text", () => {
    expect(
      classifyResourceApplyFailure(
        Object.assign(new Error("localized text"), { status: 409 }),
      ),
    ).toBe("conflict");
    expect(
      classifyResourceApplyFailure(
        Object.assign(new Error("anything"), {
          response: {
            status: 409,
            data: { error: { code: "conflict" } },
          },
        }),
      ),
    ).toBe("conflict");
    expect(
      classifyResourceApplyFailure(
        Object.assign(new Error("anything"), { status: 403 }),
      ),
    ).toBe("forbidden");
    expect(
      classifyResourceApplyFailure(
        Object.assign(new Error("anything"), { status: 503 }),
      ),
    ).toBe("retryable");
  });

  it("offers an in-place retry after a terminal YAML load failure", () => {
    const refetch = vi.fn();
    mockedGetYaml.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: Object.assign(new Error("cluster temporarily offline"), {
        status: 503,
      }),
      refetch,
    } as unknown as ReturnType<typeof hooks.useK8sGetYaml>);

    const { getByRole } = render(
      <YamlPanel
        clusterId="c1"
        k8sPath="apis/apps/v1/namespaces/default/deployments/web"
      />,
    );
    refetch.mockClear();
    fireEvent.click(getByRole("button", { name: /retry/i }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it("offers exactly one forced mutation for an authorized 409 conflict", () => {
    loadedYaml();
    const mutate = vi.fn();
    mockedApplyYaml.mockReturnValue({
      mutate,
      isPending: false,
      isError: true,
      error: Object.assign(new Error("opaque"), { status: 409 }),
    } as unknown as ReturnType<typeof hooks.useK8sApplyYaml>);

    const { getByRole, getByText } = render(
      <YamlPanel
        clusterId="c1"
        k8sPath="apis/apps/v1/namespaces/default/deployments/web"
        forceConflictPermission={permission(true)}
      />,
    );
    fireEvent.click(getByText("Edit"));
    fireEvent.click(
      getByRole("button", { name: "Take ownership and apply" }),
    );
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(mutate).toHaveBeenCalledWith(
      expect.objectContaining({ force: true, yaml: "name: v1" }),
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
  });

  it("replaces forced takeover with the exact denial reason", () => {
    loadedYaml();
    mockedApplyYaml.mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      isError: true,
      error: Object.assign(new Error("opaque"), { status: 409 }),
    } as unknown as ReturnType<typeof hooks.useK8sApplyYaml>);

    const { getByText, queryByRole } = render(
      <YamlPanel
        clusterId="c1"
        k8sPath="apis/apps/v1/namespaces/default/deployments/web"
        forceConflictPermission={permission(false)}
      />,
    );
    fireEvent.click(getByText("Edit"));
    expect(getByText("Requires workloads:manage on cluster c1.")).toBeTruthy();
    expect(
      queryByRole("button", { name: "Take ownership and apply" }),
    ).toBeNull();
  });

  it("preserves edits and does not offer retry or takeover on 403", () => {
    loadedYaml();
    const mutate = vi.fn();
    mockedApplyYaml.mockReturnValue({
      mutate,
      isPending: false,
      isError: true,
      error: Object.assign(new Error("opaque"), { status: 403 }),
    } as unknown as ReturnType<typeof hooks.useK8sApplyYaml>);

    const { getByTestId, getByText, queryByRole } = render(
      <YamlPanel
        clusterId="c1"
        k8sPath="apis/apps/v1/namespaces/default/deployments/web"
        forceConflictPermission={permission(false)}
      />,
    );
    fireEvent.click(getByText("Edit"));
    fireEvent.change(getByTestId("yaml-editor"), {
      target: { value: "name: preserved" },
    });
    expect(getByText(/Permission required/)).toBeTruthy();
    expect((getByTestId("yaml-editor") as HTMLTextAreaElement).value).toBe(
      "name: preserved",
    );
    expect(queryByRole("button", { name: /retry apply/i })).toBeNull();
    expect(
      queryByRole("button", { name: /take ownership/i }),
    ).toBeNull();
    expect(mutate).not.toHaveBeenCalled();
  });

  it("retries a transient apply once while retaining the reviewed YAML", () => {
    loadedYaml();
    const mutate = vi.fn();
    mockedApplyYaml.mockReturnValue({
      mutate,
      isPending: false,
      isError: true,
      error: Object.assign(new Error("offline"), { status: 503 }),
    } as unknown as ReturnType<typeof hooks.useK8sApplyYaml>);

    const { getByRole, getByText } = render(
      <YamlPanel
        clusterId="c1"
        k8sPath="apis/apps/v1/namespaces/default/deployments/web"
      />,
    );
    fireEvent.click(getByText("Edit"));
    fireEvent.click(getByRole("button", { name: "Retry apply" }));
    expect(mutate).toHaveBeenCalledTimes(1);
    expect(mutate).toHaveBeenCalledWith(
      expect.objectContaining({
        clusterId: "c1",
        path: "apis/apps/v1/namespaces/default/deployments/web",
        yaml: "name: v1",
      }),
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
  });
});
