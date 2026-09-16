import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { PrincipalPicker } from "./principal-picker";

const materialize = vi.fn();

vi.mock("@/lib/hooks/rbac", () => ({
  usePrincipalSearch: vi.fn(() => ({
    data: {
      principals: [
        {
          kind: "local",
          user_id: "user-local",
          email: "local@example.com",
          username: "local",
          display_name: "Local Operator",
        },
        {
          kind: "external",
          connector_id: "connector-id",
          connector_name: "Employees",
          connector_type: "ldap",
          subject: "stable-42",
          email: "ada@example.com",
          username: "ada",
          display_name: "Ada Lovelace",
        },
      ],
      connectors: [
        {
          connector_id: "unsupported-id",
          connector_name: "Partners",
          connector_type: "oidc",
          supported: false,
          error: "principal discovery is unsupported",
        },
      ],
    },
    isFetching: false,
    isError: false,
  })),
  useMaterializePrincipal: vi.fn(() => ({
    mutateAsync: materialize,
    isPending: false,
  })),
}));

describe("PrincipalPicker", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    materialize.mockResolvedValue({
      id: "principal-id",
      user_id: "user-pending",
      kind: "pending",
      connector_id: "connector-id",
      email: "ada@example.com",
      username: "ada",
      display_name: "Ada Lovelace",
    });
  });

  it("distinguishes local and external identities and reports unsupported connectors", async () => {
    const onChange = vi.fn();
    render(<PrincipalPicker value="" onChange={onChange} />);

    fireEvent.change(
      screen.getByRole("searchbox", {
        name: "Search local and external identities",
      }),
      { target: { value: "ada" } },
    );
    expect(screen.getByText("Local Operator")).toBeInTheDocument();
    expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
    expect(screen.getByText("Local")).toBeInTheDocument();
    expect(screen.getByText("External")).toBeInTheDocument();
    expect(
      screen.getByText(/Partners: principal discovery is unsupported/),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByText("Ada Lovelace"));
    await waitFor(() => {
      expect(materialize).toHaveBeenCalledWith({
        connectorId: "connector-id",
        subject: "stable-42",
      });
      expect(onChange).toHaveBeenCalledWith("user-pending");
    });
    expect(screen.getByText("Pending sign-in")).toBeInTheDocument();
  });
});
