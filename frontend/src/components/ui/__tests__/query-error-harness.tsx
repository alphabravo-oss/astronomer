/**
 * Error-state harness (P023.9): render a page with every `useQuery` forced
 * to fail and assert the page shows a real error surface — never an empty
 * state that would make "the API is down" look identical to "there is
 * nothing here yet".
 *
 * The mocking itself must live in each consuming *.test.tsx file (Vitest
 * only hoists `vi.mock` within the file that calls it), so this module only
 * exports the shared assertion + a `failEveryQuery` factory the mock
 * delegates to. See query-error-harness.test.tsx for the wiring and the ten
 * page cases.
 */
import { screen } from "@testing-library/react";

/** True for every query key — the default "the whole page is broken" case. */
export function failEveryQuery(): boolean {
  return true;
}

/**
 * Asserts the rendered page surfaces the failure (a `role="alert"` node, or
 * a StatePanel-style danger title) and never renders one of the page's own
 * empty-state titles — a failed fetch must never look like "nothing here".
 */
export function expectErrorSurfaceNotEmptyState(
  emptyStateTitles: readonly string[] = [],
): void {
  const alerts = screen.queryAllByRole("alert");
  expect(alerts.length).toBeGreaterThan(0);
  for (const title of emptyStateTitles) {
    expect(screen.queryByText(title)).not.toBeInTheDocument();
  }
}
