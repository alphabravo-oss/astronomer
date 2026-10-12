import type { Mock } from "vitest";
import { ReactNode } from "react";
import { render, screen, fireEvent, within, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { InhibitionPanel } from "./-inhibition-panel";
import type { AlertInhibition } from "@/types";

const mutations = vi.hoisted(() => ({ update: vi.fn() }));

vi.mock("./-inhibition-hooks", () => ({
  useInhibitions: vi.fn(),
  useCreateInhibition: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateInhibition: () => ({
    mutateAsync: mutations.update,
    isPending: false,
  }),
  useDeleteInhibition: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

import { useInhibitions } from "./-inhibition-hooks";

const mockedUseInhibitions = useInhibitions as Mock;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const sample: AlertInhibition = {
  id: "i1",
  name: "Suppress node alerts",
  sourceMatchers: [
    { label: "alertname", value: "ClusterDown", isRegex: false },
  ],
  targetMatchers: [{ label: "severity", value: "warn.*", isRegex: true }],
  equalLabels: ["cluster"],
  enabled: true,
  createdAt: "2026-07-01T00:00:00Z",
  updatedAt: "2026-07-01T00:00:00Z",
};

describe("InhibitionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mutations.update.mockReset();
  });

  it("renders inhibition rows with regex-aware matcher chips", () => {
    mockedUseInhibitions.mockReturnValue({
      data: [sample],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    render(<InhibitionPanel />, { wrapper });

    expect(screen.getByText("Suppress node alerts")).toBeInTheDocument();
    // Regex matcher uses `=~`; exact matcher uses `=`.
    expect(screen.getByText("severity=~warn.*")).toBeInTheDocument();
    expect(screen.getByText("alertname=ClusterDown")).toBeInTheDocument();
  });

  it("keeps safe server field errors visible and focuses the matcher control", async () => {
    mockedUseInhibitions.mockReturnValue({
      data: [sample],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    mutations.update.mockRejectedValueOnce(
      Object.assign(new Error("private-fixture"), {
        response: {
          status: 422,
          data: {
            error: {
              code: "validation_error",
              message: "private-fixture",
              fields: [
                {
                  field: "source_matchers",
                  rule: "invalid_regex",
                  message: "private-fixture",
                },
                { field: "password", message: "private-fixture" },
              ],
            },
          },
        },
      }),
    );
    render(<InhibitionPanel />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Edit inhibition" }));
    await act(async () =>
      fireEvent.submit(screen.getByLabelText("Name").closest("form")!),
    );
    expect(mutations.update).toHaveBeenCalledTimes(1);
    const summary = screen.getByRole("alert");
    expect(summary).toHaveFocus();
    const link = within(summary).getByRole("button", {
      name: "Source matchers contain an invalid regular expression.",
    });
    expect(summary).not.toHaveTextContent("private-fixture");
    const control = screen.getByRole("textbox", {
      name: "Source matchers label",
    });
    control.scrollIntoView = vi.fn();
    fireEvent.click(link);
    expect(control).toHaveFocus();
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Updated rule" },
    });
    expect(summary).toHaveTextContent(
      "Source matchers contain an invalid regular expression.",
    );
    expect(mutations.update).toHaveBeenCalledTimes(1);
  });

  it("adds an explicit empty matcher value and submits it unchanged", async () => {
    mockedUseInhibitions.mockReturnValue({
      data: [sample],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    mutations.update.mockResolvedValueOnce({});
    render(<InhibitionPanel />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Edit inhibition" }));
    fireEvent.change(
      screen.getByRole("textbox", { name: "Source matchers label" }),
      { target: { value: "namespace" } },
    );
    const add = screen.getAllByRole("button", { name: "Add matcher" })[0];
    expect(add).toBeEnabled();
    fireEvent.click(add);
    await act(async () =>
      fireEvent.submit(screen.getByLabelText("Name").closest("form")!),
    );
    expect(mutations.update).toHaveBeenCalledTimes(1);
    expect(
      mutations.update.mock.calls[0][0].body.source_matchers,
    ).toContainEqual({ label: "namespace", value: "", is_regex: false });
  });

  it("opens the create modal when the create button is clicked", () => {
    mockedUseInhibitions.mockReturnValue({
      data: [],
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    render(<InhibitionPanel />, { wrapper });

    expect(
      screen.queryByText("Create Inhibition Rule"),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Create Inhibition/i }));
    expect(screen.getByText("Create Inhibition Rule")).toBeInTheDocument();
    // Source + target matcher editors are present (label also appears as an
    // empty-table column header, so there are 2 matches for each).
    expect(screen.getAllByText("Source matchers").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Target matchers").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Equal labels").length).toBeGreaterThan(0);
  });
});
