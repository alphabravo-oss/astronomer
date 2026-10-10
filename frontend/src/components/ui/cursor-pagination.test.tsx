import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
} from "@testing-library/react";
import { useEffect } from "react";
import { CursorPagination, useCursorPagination } from "./cursor-pagination";
import type { PaginationMetadata } from "@/types";

function Fixture({
  scope = "project-a",
  limit = 25,
  metadata,
  fetching = false,
  error = false,
  onRequest,
}: {
  scope?: string;
  limit?: number;
  metadata: PaginationMetadata;
  fetching?: boolean;
  error?: boolean;
  onRequest?: (scope: string, limit: number, cursor: string) => void;
}) {
  const control = useCursorPagination(scope, limit);
  useEffect(() => {
    onRequest?.(scope, control.params.limit, control.params.cursor);
  }, [scope, control.params.limit, control.params.cursor, onRequest]);
  return (
    <>
      <output data-testid="request">{JSON.stringify(control.params)}</output>
      <CursorPagination
        control={control}
        label="charts"
        query={{
          data: { pagination: metadata },
          isFetching: fetching,
          isError: error,
        }}
      />
    </>
  );
}
const continuation = {
  limit: 25,
  offset: 10000,
  has_more: true,
  next_offset: 10025,
  next_cursor: "opaque-past-ten-thousand",
};

it("uses opaque continuations beyond the offset ceiling and revisits only known tokens", () => {
  const { rerender } = render(<Fixture metadata={continuation} />);
  expect(screen.getByTestId("request")).toHaveTextContent(
    '{"limit":25,"cursor":""}',
  );
  fireEvent.click(screen.getByRole("button", { name: "Next charts page" }));
  expect(screen.getByTestId("request")).toHaveTextContent(
    '{"limit":25,"cursor":"opaque-past-ten-thousand"}',
  );
  rerender(
    <Fixture metadata={{ ...continuation, next_cursor: "next-opaque" }} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Next charts page" }));
  expect(screen.getByText("Page 3")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Previous charts page" }));
  expect(screen.getByTestId("request")).toHaveTextContent(
    '"cursor":"opaque-past-ten-thousand"',
  );
  fireEvent.click(screen.getByRole("button", { name: "Previous charts page" }));
  expect(screen.getByTestId("request")).toHaveTextContent('"cursor":""');
});

it("resets the request synchronously when scope, filters or limit change", () => {
  const { rerender } = render(
    <Fixture scope='["project-a","search-a"]' metadata={continuation} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Next charts page" }));
  rerender(
    <Fixture scope='["project-a","search-b"]' metadata={continuation} />,
  );
  expect(screen.getByTestId("request")).toHaveTextContent(
    '{"limit":25,"cursor":""}',
  );
  expect(screen.getByText("Page 1")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Next charts page" }));
  rerender(
    <Fixture
      scope='["project-a","search-b"]'
      limit={50}
      metadata={continuation}
    />,
  );
  expect(screen.getByTestId("request")).toHaveTextContent(
    '{"limit":50,"cursor":""}',
  );
});

it("stops on repeated or missing tokens and prevents navigation during fetching", () => {
  const { rerender } = render(<Fixture metadata={continuation} />);
  fireEvent.click(screen.getByRole("button", { name: "Next charts page" }));
  expect(
    screen.getByRole("button", { name: "Next charts page" }),
  ).toBeDisabled();
  rerender(<Fixture metadata={{ ...continuation, next_cursor: undefined }} />);
  expect(
    screen.getByRole("button", { name: "Next charts page" }),
  ).toBeDisabled();
  rerender(
    <Fixture
      metadata={{ ...continuation, next_cursor: "valid-next" }}
      fetching
    />,
  );
  expect(
    screen.getByRole("button", { name: "Next charts page" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Previous charts page" }),
  ).toBeDisabled();
  rerender(
    <Fixture metadata={{ ...continuation, next_cursor: "valid-next" }} error />,
  );
  expect(
    screen.getByRole("button", { name: "Next charts page" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Previous charts page" }),
  ).toBeEnabled();
});

it.each(["scope", "limit"])(
  "does not revive a continuation after a %s roundtrip without navigation",
  (changed) => {
    const onRequest = vi.fn();
    const { rerender } = render(
      <Fixture metadata={continuation} onRequest={onRequest} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Next charts page" }));
    rerender(
      <Fixture
        scope={changed === "scope" ? "project-b" : "project-a"}
        limit={changed === "limit" ? 50 : 25}
        metadata={continuation}
        onRequest={onRequest}
      />,
    );
    expect(screen.getByTestId("request")).toHaveTextContent('"cursor":""');
    rerender(<Fixture metadata={continuation} onRequest={onRequest} />);
    expect(screen.getByTestId("request")).toHaveTextContent(
      '{"limit":25,"cursor":""}',
    );
    expect(screen.getByText("Page 1")).toBeInTheDocument();
    expect(onRequest.mock.calls.slice(2)).toEqual([
      [
        changed === "scope" ? "project-b" : "project-a",
        changed === "limit" ? 50 : 25,
        "",
      ],
      ["project-a", 25, ""],
    ]);
  },
);

it("bounds previous-token history while preserving unlimited forward traversal and absolute pages", () => {
  const { result, rerender } = renderHook(
    ({ scope, limit }) => useCursorPagination(scope, limit),
    { initialProps: { scope: "project-a", limit: 25 } },
  );
  for (let page = 2; page <= 450; page++) {
    act(() => result.current.next(`opaque-page-${page}`));
    expect(result.current.page).toBe(page);
  }
  let previousCount = 0;
  for (let previous = 0; previous < 450; previous++) {
    const before = result.current.params.cursor;
    act(() => result.current.previous());
    if (result.current.params.cursor === before) break;
    previousCount++;
  }
  expect(previousCount).toBe(128);
  expect(result.current.page).toBe(322);
  expect(result.current.params.cursor).toBe("opaque-page-322");
  render(
    <CursorPagination
      control={result.current}
      label="charts"
      query={{
        data: {
          pagination: { ...continuation, next_cursor: "next-unvisited" },
        },
        isError: false,
        isFetching: false,
      }}
    />,
  );
  expect(
    screen.getByRole("button", { name: "Previous charts page" }),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Next charts page" }),
  ).toBeEnabled();
  expect(screen.getByText("Page 322")).toBeInTheDocument();
  expect(
    screen.getByText("Earlier pages are outside the 128-page history."),
  ).toBeInTheDocument();
  act(() => result.current.next("opaque-new-forward-page"));
  expect(result.current.page).toBe(323);
  rerender({ scope: "project-b", limit: 50 });
  expect(result.current.page).toBe(1);
  expect(result.current.params).toEqual({ limit: 50, cursor: "" });
});
