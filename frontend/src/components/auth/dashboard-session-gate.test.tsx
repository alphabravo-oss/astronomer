import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DashboardSessionGate } from "./dashboard-session-gate";
import { useCurrentUser } from "@/lib/hooks/auth";

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  updateUser: vi.fn(),
  refetch: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => mocks.navigate,
}));
vi.mock("@/lib/hooks/auth", () => ({ useCurrentUser: vi.fn() }));
vi.mock("@/lib/store", () => ({
  useAuthStore: (select: (state: unknown) => unknown) =>
    select({ updateUser: mocks.updateUser }),
}));
vi.mock("@/components/ui/empty-state", () => ({
  StatePanel: ({
    title,
    onAction,
  }: {
    title: string;
    onAction: () => void;
  }) => (
    // eslint-disable-next-line no-restricted-syntax -- isolated test retry control
    <button onClick={onAction}>{title}</button>
  ),
}));

function mount(state: Record<string, unknown>) {
  vi.mocked(useCurrentUser).mockReturnValue({
    isFetched: true,
    isError: false,
    error: null,
    refetch: mocks.refetch,
    ...state,
  } as never);
  return render(
    <DashboardSessionGate>
      <div>Protected content</div>
    </DashboardSessionGate>,
  );
}

beforeEach(() => vi.clearAllMocks());
describe("dashboard session qualification", () => {
  it("keeps an initial network failure behind a retry gate without logging out", () => {
    mount({ isError: true, error: { status: 0 } });
    expect(screen.queryByText("Protected content")).not.toBeInTheDocument();
    expect(mocks.navigate).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Unable to check your session" }),
    );
    expect(mocks.refetch).toHaveBeenCalledOnce();
  });
  it("preserves an authenticated view when a background identity read loses the network", () => {
    mount({ data: { id: "operator" }, isError: true, error: { status: 0 } });
    expect(screen.getByText("Protected content")).toBeInTheDocument();
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
  it.each([401, 403])(
    "blocks cached identity after an explicit %s rejection",
    (status) => {
      mount({ data: { id: "operator" }, isError: true, error: { status } });
      expect(screen.queryByText("Protected content")).not.toBeInTheDocument();
      expect(mocks.navigate).toHaveBeenCalledWith({
        to: "/auth/login",
        replace: true,
      });
      expect(mocks.updateUser).not.toHaveBeenCalled();
    },
  );
  it("blocks all children until password rotation is complete", () => {
    mount({ data: { id: "operator", must_change_password: true } });
    expect(screen.queryByText("Protected content")).not.toBeInTheDocument();
    expect(mocks.navigate).toHaveBeenCalledWith({
      to: "/auth/change-password",
      replace: true,
    });
  });
  it("blocks children while the first identity request is unresolved", () => {
    mount({ isFetched: false });
    expect(screen.queryByText("Protected content")).not.toBeInTheDocument();
    expect(mocks.navigate).not.toHaveBeenCalled();
  });
});
