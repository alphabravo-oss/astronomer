import { fireEvent, render, screen } from "@testing-library/react";
import { CursorPagination, useCursorPagination } from "./cursor-pagination";
import type { PaginationMetadata } from "@/types";

function Fixture({
  scope = "project-a",
  limit = 25,
  metadata,
  fetching = false,
  error = false,
}: {
  scope?: string;
  limit?: number;
  metadata: PaginationMetadata;
  fetching?: boolean;
  error?: boolean;
}) {
  const control = useCursorPagination(scope, limit);
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
