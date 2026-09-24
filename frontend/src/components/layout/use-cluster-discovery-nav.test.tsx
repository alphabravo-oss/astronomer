import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { getCompleteResourceDiscovery } from "@/lib/api/resources";
import { useAuthStore } from "@/lib/store";
import { clusterDiscoveryFromDefinitions } from "./cluster-discovery-model";
import { useClusterDiscovery } from "./use-cluster-discovery-nav";

vi.mock("@/lib/api/resources", () => ({ getCompleteResourceDiscovery: vi.fn() }));

export function crd(group: string, plural: string, kind: string) {
  return {
    spec: {
      group,
      scope: "Namespaced",
      names: { plural, kind },
      versions: [{ name: "v1", served: true, storage: true }],
    },
  };
}

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      {children}
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.mocked(getCompleteResourceDiscovery).mockReset();
  useAuthStore.setState({ user: { id: "u-1", isSuperuser: true } as never });
});

it("maps served CRDs into groups and caches the single discovery request", async () => {
  vi.mocked(getCompleteResourceDiscovery).mockResolvedValue({
    clusterId: "c-1", resources: [], partial: false, errors: {}, crdContinue: "",
    crds: [
      summary("cert-manager.io", "certificates", "Certificate"),
      summary("cert-manager.io", "issuers", "Issuer"),
      summary("monitoring.coreos.com", "prometheuses", "Prometheus"),
    ],
  });
  const { result } = renderHook(() => useClusterDiscovery("c-1"), { wrapper });
  await waitFor(() => expect(result.current.isLoading).toBe(false));
  expect(result.current.groups.size).toBe(2);
  expect(result.current.crdsByGroup.get("cert-manager.io")).toHaveLength(2);
  expect(result.current.kinds.has("cert-manager.io/Certificate")).toBe(true);
  expect(getCompleteResourceDiscovery).toHaveBeenCalledTimes(1);
});

it("does not query outside cluster context or without permission", () => {
  renderHook(() => useClusterDiscovery(), { wrapper });
  useAuthStore.setState({ user: null });
  const { result } = renderHook(() => useClusterDiscovery("c-1"), { wrapper });
  expect(getCompleteResourceDiscovery).not.toHaveBeenCalled();
  expect(result.current.isError).toBe(true);
});

it("distinguishes failed discovery from a cluster without CRDs", async () => {
  vi.mocked(getCompleteResourceDiscovery).mockRejectedValue(new Error("Forbidden"));
  const { result } = renderHook(() => useClusterDiscovery("c-1"), { wrapper });
  await waitFor(() => expect(result.current.isError).toBe(true));
  expect(result.current.crdsByGroup.size).toBe(0);
});

it("chooses only served versions and rejects malformed path segments", () => {
  const valid = crd("cert-manager.io", "certificates", "Certificate");
  valid.spec.versions = [
    { name: "v1beta1", served: false, storage: true },
    { name: "v1", served: true, storage: false },
  ];
  const discovery = clusterDiscoveryFromDefinitions([
    valid,
    valid,
    crd("../bad", "secrets", "Secret"),
    {
      spec: {
        ...valid.spec,
        names: { plural: "issuers", kind: "Issuer" },
        versions: [],
      },
    },
  ]);
  expect(discovery.crdsByGroup.get("cert-manager.io")).toEqual([
    {
      group: "cert-manager.io",
      plural: "certificates",
      kind: "Certificate",
      version: "v1",
      servedVersions: ["v1"],
      namespaced: true,
    },
  ]);
});

function summary(group: string, plural: string, kind: string) { return { group, plural, kind, scope: "Namespaced", versions: [{ name: "v1", storage: true }] }; }
