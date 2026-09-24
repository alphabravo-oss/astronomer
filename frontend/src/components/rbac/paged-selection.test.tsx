import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { RemoteRolePicker } from "./remote-role-picker";
import { RolesTab } from "@/routes/dashboard/rbac/-roles-tab";
import { UsersTab } from "@/routes/dashboard/rbac/-users-tab";
import { RemoteStoragePicker } from "@/components/backups/remote-storage-picker";
import { getRolePage } from "@/lib/api/rbac-role-page";
import { getUsers } from "@/lib/api/user-settings";
import { b2ListStorageLocations } from "@/lib/api/backups";
import { queryKeys } from "@/lib/query-keys";
import { b2Keys } from "@/components/backups/hooks";

const rights = vi.hoisted(() => ({ read: true }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: rights.read }),
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("@/lib/api/rbac-role-page", () => ({ getRolePage: vi.fn() }));
vi.mock("@/lib/api/backups", () => ({ b2ListStorageLocations: vi.fn() }));
vi.mock("@/lib/api/user-settings", () => ({ getUsers: vi.fn() }));

function mount(child: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(<QueryClientProvider client={client}>{child}</QueryClientProvider>);
  return client;
}
function page<T>(data: T[], offset = 0) {
  return {
    data,
    pagination: {
      limit: 25,
      offset,
      total: 226,
      has_more: offset < 225,
      next_offset: offset < 225 ? offset + 25 : null,
    },
  };
}
const role = {
  id: "role-late",
  name: "Late role",
  displayName: "Late role",
  createdAt: "2026-09-22T00:00:00Z",
  rules: [],
};
const user = {
  id: "user-late",
  username: "late-user",
  displayName: "Late user",
  email: "late@example.test",
  provider: "local",
  enabled: true,
  createdAt: "2026-09-22T00:00:00Z",
};
const actions = {
  onEdit: vi.fn(),
  onDuplicate: vi.fn(),
  onDelete: vi.fn(),
  onResetPassword: vi.fn(),
};
beforeEach(() => {
  vi.clearAllMocks();
  rights.read = true;
});

it.each(["global", "cluster", "project"] as const)(
  "pages %s role screens past the old 200-row cap",
  async (scope) => {
    vi.mocked(getRolePage).mockImplementation(async (_scope, offset) =>
      page(offset === 225 ? [role] : [], offset),
    );
    mount(<RolesTab scope={scope} {...actions} />);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
    );
    for (let i = 1; i <= 9; i++) {
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
      );
      fireEvent.click(screen.getByRole("button", { name: "Next page" }));
      await waitFor(() =>
        expect(getRolePage).toHaveBeenCalledWith(
          scope,
          i * 25,
          expect.any(AbortSignal),
        ),
      );
    }
    expect(await screen.findAllByText("Late role")).not.toHaveLength(0);
  },
);

it("selects a role from a later server page", async () => {
  vi.mocked(getRolePage).mockImplementation(async (_scope, offset) =>
    page(offset ? [role] : [], offset),
  );
  const change = vi.fn();
  mount(<RemoteRolePicker scope="project" value="" onChange={change} />);
  expect(getRolePage).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Role" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Select Late role" }),
  );
  expect(change).toHaveBeenCalledWith("role-late");
});

it("hides cached roles after a denied refetch", async () => {
  vi.mocked(getRolePage).mockResolvedValue(page([role]));
  const client = mount(<RolesTab scope="global" {...actions} />);
  await screen.findAllByText("Late role");
  vi.mocked(getRolePage).mockRejectedValue({ status: 403 });
  await act(() =>
    client.invalidateQueries({
      queryKey: queryKeys.rbac.rolePage("global", 0),
    }),
  );
  expect(await screen.findByText(/permission required/i)).toBeVisible();
  expect(screen.queryByText("Late role")).not.toBeInTheDocument();
});

it("sends user search to the server and resets pagination", async () => {
  vi.mocked(getUsers).mockImplementation(async (params) =>
    page([user] as never[], ((params?.page ?? 1) - 1) * 25),
  );
  mount(<UsersTab {...actions} />);
  await screen.findByText("Late user");
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await waitFor(() =>
    expect(getUsers).toHaveBeenLastCalledWith({
      page: 2,
      pageSize: 25,
      search: "",
    }),
  );
  fireEvent.change(screen.getByPlaceholderText("Search users..."), {
    target: { value: "beyond-200" },
  });
  await waitFor(() =>
    expect(getUsers).toHaveBeenLastCalledWith({
      page: 1,
      pageSize: 25,
      search: "beyond-200",
    }),
  );
});

it("hides cached users after a denied refetch", async () => {
  vi.mocked(getUsers).mockResolvedValue(page([user] as never[]));
  const client = mount(<UsersTab {...actions} />);
  await screen.findByText("Late user");
  vi.mocked(getUsers).mockRejectedValue({ status: 403 });
  await act(() => client.invalidateQueries({ queryKey: queryKeys.users.all }));
  expect(await screen.findByText(/permission required/i)).toBeVisible();
  expect(screen.queryByText("Late user")).not.toBeInTheDocument();
});

it("pages backup storage and selects a later configuration", async () => {
  vi.mocked(b2ListStorageLocations).mockImplementation(async (params) =>
    page(
      params?.page === 2
        ? ([
            { id: "storage-late", name: "Archive", bucket: "metrics" },
          ] as never[])
        : [],
      ((params?.page ?? 1) - 1) * 25,
    ),
  );
  const change = vi.fn();
  mount(
    <RemoteStoragePicker value="" onChange={change} label="Object storage" />,
  );
  expect(b2ListStorageLocations).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Find storage configuration" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Select Archive" }),
  );
  expect(b2ListStorageLocations).toHaveBeenLastCalledWith({
    page: 2,
    page_size: 25,
  });
  expect(change).toHaveBeenCalledWith("storage-late");
});

it("does not enumerate without read permission and retains known storage ID entry", () => {
  rights.read = false;
  const change = vi.fn();
  mount(
    <>
      <RemoteStoragePicker value="" onChange={change} label="Object storage" />
      <RolesTab scope="global" {...actions} />
      <UsersTab {...actions} />
    </>,
  );
  expect(
    screen.getByRole("button", { name: "Find storage configuration" }),
  ).toBeDisabled();
  expect(getRolePage).not.toHaveBeenCalled();
  expect(getUsers).not.toHaveBeenCalled();
  expect(b2ListStorageLocations).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Object storage"), {
    target: { value: "known-id" },
  });
  expect(change).toHaveBeenCalledWith("known-id");
});

it("hides cached storage choices after a denied refresh", async () => {
  vi.mocked(b2ListStorageLocations).mockResolvedValue(
    page([
      { id: "private-storage", name: "Private archive", bucket: "metrics" },
    ] as never[]),
  );
  const client = mount(
    <RemoteStoragePicker value="" onChange={vi.fn()} label="Object storage" />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Find storage configuration" }),
  );
  await screen.findByRole("button", { name: "Select Private archive" });
  vi.mocked(b2ListStorageLocations).mockRejectedValue({ status: 403 });
  await act(() => client.invalidateQueries({ queryKey: b2Keys.all }));
  expect(await screen.findByText(/permission required/i)).toBeVisible();
  expect(screen.queryByText("Private archive")).not.toBeInTheDocument();
});

it("hides cached role counts and choices when read permission is revoked while open", async () => {
  vi.mocked(getRolePage).mockResolvedValue(page([role]));
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const tree = () => (
    <QueryClientProvider client={client}>
      <RemoteRolePicker scope="global" value="" onChange={vi.fn()} />
    </QueryClientProvider>
  );
  const view = render(tree());
  fireEvent.click(screen.getByRole("button", { name: "Role" }));
  await screen.findByRole("button", { name: "Select Late role" });
  expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled();
  rights.read = false;
  view.rerender(tree());
  expect(screen.getByText(/permission required/i)).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Next page" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText("Late role")).not.toBeInTheDocument();
});
