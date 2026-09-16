import { fireEvent, render, screen } from "@testing-library/react";

import { QueryStates, type QueryState } from "@/components/ui/query-states";

function query<T>(overrides: Partial<QueryState<T>> = {}): QueryState<T> {
  return {
    data: undefined,
    error: undefined,
    isError: false,
    isLoading: false,
    refetch: vi.fn(),
    ...overrides,
  };
}

describe("QueryStates", () => {
  it("renders the successful data through its render function", () => {
    render(
      <QueryStates query={query({ data: ["cluster-a"] })}>
        {(clusters) => <p>{clusters.join(", ")}</p>}
      </QueryStates>,
    );

    expect(screen.getByText("cluster-a")).toBeInTheDocument();
  });

  it("renders a loading state only before data exists", () => {
    const { rerender } = render(
      <QueryStates
        query={query({ isLoading: true })}
        loadingTitle="Loading clusters"
      >
        loaded
      </QueryStates>,
    );

    expect(screen.getByText("Loading clusters")).toBeInTheDocument();

    rerender(
      <QueryStates query={query({ data: ["cached"], isLoading: true })}>
        cached data
      </QueryStates>,
    );
    expect(screen.getByText("cached data")).toBeInTheDocument();
  });

  it.each([401, 403])("renders permission state for status %s", (status) => {
    render(
      <QueryStates
        query={query({ isError: true, error: { status } })}
        permission="clusters:read"
      >
        hidden
      </QueryStates>,
    );

    expect(screen.getByText("Permission required")).toBeInTheDocument();
    expect(screen.getByText("clusters:read")).toBeInTheDocument();
  });

  it("renders a retryable offline state for transport errors", () => {
    const refetch = vi.fn();
    render(
      <QueryStates
        query={query({ isError: true, error: { status: 0 }, refetch })}
      >
        hidden
      </QueryStates>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(screen.getByText("Connection unavailable")).toBeInTheDocument();
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("does not misclassify an unstructured application error as offline", () => {
    render(
      <QueryStates
        query={query({ isError: true, error: new Error("invalid payload") })}
      >
        hidden
      </QueryStates>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("invalid payload");
    expect(
      screen.queryByText("Connection unavailable"),
    ).not.toBeInTheDocument();
  });

  it("renders an API failure separately from empty data", () => {
    render(
      <QueryStates
        query={query({
          isError: true,
          error: { status: 409, message: "resource conflict" },
        })}
      >
        hidden
      </QueryStates>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("resource conflict");
  });

  it("supports explicit not-found and empty content", () => {
    const { rerender } = render(
      <QueryStates
        query={query({ isError: true, error: { status: 404 } })}
        notFound={<p>Cluster not found</p>}
      >
        hidden
      </QueryStates>,
    );
    expect(screen.getByText("Cluster not found")).toBeInTheDocument();

    rerender(
      <QueryStates
        query={query({ data: [] as string[] })}
        isEmpty={(rows) => rows.length === 0}
        empty={<p>No clusters yet</p>}
      >
        hidden
      </QueryStates>,
    );
    expect(screen.getByText("No clusters yet")).toBeInTheDocument();
  });
});
