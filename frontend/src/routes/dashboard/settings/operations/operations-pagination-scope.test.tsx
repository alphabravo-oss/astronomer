import { act, renderHook } from "@testing-library/react";
import { useOperationsPage } from "./-pagination";

it.each(["queue", "status"])(
  "persists the %s reset across an A→B→A roundtrip with no page interaction in B",
  (kind) => {
    const first = kind === "queue" ? "default" : "dead";
    const second = kind === "queue" ? "critical" : "failed";
    const { result, rerender } = renderHook(
      ({ scope }) => useOperationsPage(scope),
      { initialProps: { scope: first } },
    );
    act(() => result.current.change({ pageIndex: 1, pageSize: 100 }));
    expect(result.current.pagination).toEqual({ pageIndex: 1, pageSize: 100 });
    rerender({ scope: second });
    expect(result.current.pagination).toEqual({ pageIndex: 0, pageSize: 100 });
    rerender({ scope: first });
    expect(result.current.pagination).toEqual({ pageIndex: 0, pageSize: 100 });
  },
);
it("uses constant retained memory and never clamps an accepted page to an earlier one", () => {
  const { result } = renderHook(() => useOperationsPage("fixture"));
  for (let index = 1; index <= 130; index++)
    act(() => result.current.change({ pageIndex: index, pageSize: 100 }));
  expect(result.current.pagination).toEqual({ pageIndex: 130, pageSize: 100 });
  for (const page of [
    { pageIndex: -1, pageSize: 100 },
    { pageIndex: 1.5, pageSize: 100 },
    { pageIndex: 1, pageSize: 20 },
  ]) {
    expect(() => result.current.change(page)).toThrow(
      "Unsupported operations page",
    );
  }
  expect(result.current.pagination).toEqual({ pageIndex: 130, pageSize: 100 });
});
