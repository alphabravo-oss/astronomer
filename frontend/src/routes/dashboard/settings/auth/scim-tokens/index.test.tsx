import type { Mock } from "vitest";
import { ReactNode } from "react";
import { act, render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore } from "@/lib/store";
import SCIMTokensPage from "./-page";
import type { SCIMToken } from "@/types";

const navigate = vi.fn();
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
    useNavigate: () => navigate,
  };
});


vi.mock("@/lib/toast", () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastApiError: vi.fn(),
}));

vi.mock("./-hooks", () => ({
  useSCIMTokens: vi.fn(),
  useCreateSCIMToken: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useRevokeSCIMToken: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

import { useSCIMTokens } from "./-hooks";

const mockedList = useSCIMTokens as Mock;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function setSuperuser() {
  act(() => {
    useAuthStore.setState({
      isAuthenticated: true,
      user: {
        id: "admin",
        email: "a@b.io",
        username: "admin",
        is_active: true,
        is_superuser: true,
        roles: { global: [], cluster: [], project: [] },
      } as never,
    });
  });
}

const token: SCIMToken = {
  id: "t1",
  name: "okta-provisioning",
  prefix: "astro_scim_ab",
  lastUsedAt: null,
  createdAt: "2026-07-01T00:00:00Z",
  expiresAt: "2026-09-29T00:00:00Z",
  revokedAt: null,
};

describe("SCIMTokensPage", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => {
    act(() => useAuthStore.setState({ user: null, isAuthenticated: false }));
  });

  it("renders token metadata and navigates to the mint route for a superuser", () => {
    setSuperuser();
    mockedList.mockReturnValue({
      data: [token],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    render(<SCIMTokensPage />, { wrapper });

    expect(screen.getByText("okta-provisioning")).toBeInTheDocument();
    expect(screen.getByText("Never")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Mint Token/i }));
    expect(navigate).toHaveBeenCalledWith({
      to: "/dashboard/settings/auth/scim-tokens/new",
    });
  });
});
