import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { toastApiError } from "@/lib/toast";
import {
  clusterDiscoveryFromDefinitions,
  type ClusterDiscovery,
} from "@/components/layout/cluster-discovery-model";

vi.mock("@/lib/toast", () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastApiError: vi.fn(),
}));

const mutateAsync = vi.fn().mockResolvedValue([]);
let discovery: ClusterDiscovery;
vi.mock("@/components/layout/use-cluster-discovery-nav", () => ({
  useClusterDiscovery: () => discovery,
}));

vi.mock("@/lib/hooks/kubernetes-proxy", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/hooks/kubernetes-proxy")>();
  return {
    ...actual,
    useK8sCreateBatch: () => ({ mutateAsync, isPending: false }),
    useResourceSchema: () => ({
      data: undefined,
      isLoading: false,
      isError: false,
    }),
  };
});

// Stand in for the lazy-loaded Monaco editor with a plain textarea so tests
// don't need a real editor instance; YamlEditor only relies on
// value/onChange/onMount.
vi.mock("@/components/ui/monaco-editor", () => ({
  default: ({
    value,
    onChange,
  }: {
    value: string;
    onChange?: (next: string) => void;
  }) => (
    <textarea
      aria-label="yaml-editor"
      value={value}
      onChange={(event) => onChange?.(event.target.value)}
    />
  ),
}));

import { CreateResourceDialog } from "./create-resource-dialog";

function wrap(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

describe("CreateResourceDialog without a templateKey (Import YAML)", () => {
  beforeEach(() => {
    mutateAsync.mockClear();
    vi.mocked(toastApiError).mockClear();
    discovery = {
      ...clusterDiscoveryFromDefinitions([
        {
          spec: {
            group: "cert-manager.io",
            scope: "Namespaced",
            names: { plural: "certificates", kind: "Certificate" },
            versions: [{ name: "v1", served: true }],
          },
        },
      ]),
      isLoading: false,
      isError: false,
    };
  });

  it("opens directly in YAML mode with the guided/yaml toggle hidden", async () => {
    render(
      wrap(
        <CreateResourceDialog
          open
          onClose={vi.fn()}
          clusterId="cluster-1"
          title="Import YAML"
        />,
      ),
    );

    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    const editor = await screen.findByLabelText("yaml-editor");
    expect(editor).toHaveValue(
      "# Paste one or more Kubernetes manifests, separated by ---\n",
    );
  });

  it("submits two documents of different kinds as two batch items", async () => {
    render(
      wrap(
        <CreateResourceDialog
          open
          onClose={vi.fn()}
          clusterId="cluster-1"
          title="Import YAML"
        />,
      ),
    );

    const editor = await screen.findByLabelText("yaml-editor");
    fireEvent.change(editor, {
      target: {
        value: [
          "apiVersion: apps/v1",
          "kind: Deployment",
          "metadata:",
          "  name: web",
          "  namespace: default",
          "---",
          "apiVersion: v1",
          "kind: Service",
          "metadata:",
          "  name: web-svc",
          "  namespace: default",
        ].join("\n"),
      },
    });

    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    const call = mutateAsync.mock.calls[0][0] as {
      clusterId: string;
      items: Array<{ path: string; label: string }>;
    };
    expect(call.clusterId).toBe("cluster-1");
    expect(call.items).toHaveLength(2);
    expect(call.items[0].path).toBe(
      "apis/apps/v1/namespaces/default/deployments",
    );
    expect(call.items[1].path).toBe("api/v1/namespaces/default/services");
  });

  it("imports a CRD through discovery without a template or explicit API path", async () => {
    render(
      wrap(
        <CreateResourceDialog
          open
          onClose={vi.fn()}
          clusterId="cluster-1"
          title="Import YAML"
          initialYaml={
            "apiVersion: cert-manager.io/v1\nkind: Certificate\nmetadata:\n  name: web\n  namespace: team-a"
          }
        />,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledOnce());
    expect(mutateAsync.mock.calls[0][0].items[0].path).toBe(
      "apis/cert-manager.io/v1/namespaces/team-a/certificates",
    );
  });

  it.each(["isError", "isLoading"] as const)(
    "does not partially apply a mixed import when discovery %s",
    async (state) => {
      discovery[state] = true;
      render(
        wrap(
          <CreateResourceDialog
            open
            onClose={vi.fn()}
            clusterId="cluster-1"
            title="Import YAML"
            initialYaml={
              "apiVersion: v1\nkind: ConfigMap\n---\napiVersion: cert-manager.io/v1\nkind: Certificate"
            }
          />,
        ),
      );
      fireEvent.click(screen.getByRole("button", { name: "Create" }));
      await waitFor(() =>
        expect(toastApiError).toHaveBeenCalledWith(
          "Invalid resource definition",
          expect.objectContaining({
            message: expect.stringContaining(
              "Cannot determine the API endpoint for Certificate",
            ),
          }),
        ),
      );
      expect(mutateAsync).not.toHaveBeenCalled();
    },
  );
});
