/**
 * useUnsavedGuard: renders a Stay/Discard prompt while the router's
 * `useBlocker` reports the navigation as blocked, and gets out of the way
 * once the caller reports the form is no longer dirty (e.g. after a
 * successful submit).
 */
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUnsavedGuard } from "@/lib/use-unsaved-guard";

const blocker = vi.hoisted(() => ({
  status: "idle" as "idle" | "blocked",
  proceed: vi.fn(),
  reset: vi.fn(),
}));

const useBlockerMock = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useBlocker: useBlockerMock,
}));

function TestForm({ isDirty }: { isDirty: boolean }) {
  const { dialog } = useUnsavedGuard(isDirty);
  return <div>{dialog}</div>;
}

describe("useUnsavedGuard", () => {
  beforeEach(() => {
    blocker.status = "idle";
    blocker.proceed.mockClear();
    blocker.reset.mockClear();
    useBlockerMock.mockReset();
    useBlockerMock.mockImplementation(() =>
      blocker.status === "blocked"
        ? {
            status: "blocked",
            proceed: blocker.proceed,
            reset: blocker.reset,
          }
        : { status: "idle", proceed: undefined, reset: undefined },
    );
  });

  it("wires shouldBlockFn to isDirty and enables beforeunload + resolver", () => {
    render(<TestForm isDirty={true} />);
    const opts = useBlockerMock.mock.calls[0][0];
    expect(opts.shouldBlockFn()).toBe(true);
    expect(opts.enableBeforeUnload).toBe(true);
    expect(opts.withResolver).toBe(true);
  });

  it("stays hidden while the blocker is idle", () => {
    render(<TestForm isDirty={true} />);
    expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument();
  });

  it("shows the Stay/Discard dialog once the blocker reports blocked", () => {
    blocker.status = "blocked";
    render(<TestForm isDirty={true} />);
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Discard" }),
    ).toBeInTheDocument();
  });

  it("Discard proceeds with the blocked navigation", () => {
    blocker.status = "blocked";
    render(<TestForm isDirty={true} />);
    fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(blocker.proceed).toHaveBeenCalledTimes(1);
  });

  it("Stay (Cancel) resets the blocker", () => {
    blocker.status = "blocked";
    render(<TestForm isDirty={true} />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(blocker.reset).toHaveBeenCalledTimes(1);
  });

  it("is not blocked once the caller reports the form is no longer dirty (post-submit)", () => {
    render(<TestForm isDirty={false} />);
    const opts = useBlockerMock.mock.calls[0][0];
    expect(opts.shouldBlockFn()).toBe(false);
    expect(opts.enableBeforeUnload).toBe(false);
    expect(screen.queryByText("Unsaved changes")).not.toBeInTheDocument();
  });
});
