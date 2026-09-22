import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

vi.mock("@/lib/toast", () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastApiError: vi.fn(),
}));

const k8sPatchMock = vi.fn().mockResolvedValue({});
vi.mock("@/lib/api/kubernetes-proxy", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/api/kubernetes-proxy")>();
  return {
    ...actual,
    k8sPatch: (...args: unknown[]) => k8sPatchMock(...args),
  };
});

const allowedDecision = {
  allowed: true,
  reason: "",
  disabledReason: "",
};
let updateDecision = allowedDecision;
vi.mock("@/lib/permission-hooks", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/permission-hooks")>();
  return {
    ...actual,
    usePermissionDecision: (
      _resource: string,
      verb: string,
    ) => (verb === "update" ? updateDecision : allowedDecision),
    canonicalPermissionResource: (resourceType: string) => resourceType,
  };
});

import { LabelsAnnotationsEditor } from "@/components/resources/labels-annotations-editor";

function wrap(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

describe("LabelsAnnotationsEditor", () => {
  beforeEach(() => {
    k8sPatchMock.mockClear();
    updateDecision = allowedDecision;
  });

  it("renders existing labels and annotations", () => {
    render(
      wrap(
        <LabelsAnnotationsEditor
          clusterId="cluster-1"
          resourceType="deployments"
          namespace="default"
          name="api"
          labels={{ app: "api" }}
          annotations={{ note: "hello" }}
        />,
      ),
    );
    expect(screen.getByText("app")).toBeInTheDocument();
    expect(screen.getByText("note")).toBeInTheDocument();
  });

  it("disables Edit without update permission", () => {
    updateDecision = { allowed: false, reason: "denied", disabledReason: "denied" };
    render(
      wrap(
        <LabelsAnnotationsEditor
          clusterId="cluster-1"
          resourceType="deployments"
          namespace="default"
          name="api"
          labels={{ app: "api" }}
          annotations={{}}
        />,
      ),
    );
    expect(screen.getByRole("button", { name: "Edit" })).toBeDisabled();
  });

  it("removing a key and saving sends null for it in the merge patch", async () => {
    render(
      wrap(
        <LabelsAnnotationsEditor
          clusterId="cluster-1"
          resourceType="deployments"
          namespace="default"
          name="api"
          labels={{ app: "api", tier: "backend" }}
          annotations={{}}
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getByRole("button", { name: /remove tier/i }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(k8sPatchMock).toHaveBeenCalledTimes(1));
    const [, , body] = k8sPatchMock.mock.calls[0];
    expect(body).toEqual({
      metadata: {
        labels: { app: "api", tier: null },
        annotations: {},
      },
    });
  });

  it("blocks submit with an inline error on an invalid label key", async () => {
    render(
      wrap(
        <LabelsAnnotationsEditor
          clusterId="cluster-1"
          resourceType="deployments"
          namespace="default"
          name="api"
          labels={{}}
          annotations={{}}
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getAllByRole("button", { name: "Add" })[0]);
    fireEvent.change(screen.getByLabelText("Labels key"), {
      target: { value: "not a valid key!" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/key/i);
    expect(k8sPatchMock).not.toHaveBeenCalled();
  });
});
