import { fireEvent, render, screen } from "@testing-library/react";
import { OffsetPagination } from "./offset-pagination";

it("uses known continuation during a background refresh and retains error recovery", () => {
  const control = {
    params: { limit: 25, offset: 25 },
    page: 2,
    previous: vi.fn(),
    next: vi.fn(),
  };
  const query = {
    data: {
      pagination: { limit: 25, offset: 25, has_more: true, next_offset: 225 },
    },
    isFetching: true,
    isError: false,
  };
  const { rerender } = render(
    <OffsetPagination control={control} query={query} label="resources" />,
  );
  expect(
    screen.getByRole("button", { name: "Previous resources page" }),
  ).toBeEnabled();
  expect(
    screen.getByRole("button", { name: "Next resources page" }),
  ).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Next resources page" }));
  expect(control.next).toHaveBeenCalledWith(225);
  rerender(
    <OffsetPagination
      control={control}
      query={{ ...query, isError: true }}
      label="resources"
    />,
  );
  expect(
    screen.getByRole("button", { name: "Next resources page" }),
  ).toBeDisabled();
  fireEvent.click(
    screen.getByRole("button", { name: "Previous resources page" }),
  );
  expect(control.previous).toHaveBeenCalledOnce();
});
