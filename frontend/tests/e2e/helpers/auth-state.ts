import { fileURLToPath } from "node:url";

export const ADMIN_AUTH_STATE = fileURLToPath(
  new URL("../../../playwright/.auth/admin.json", import.meta.url),
);
export const READ_ONLY_AUTH_STATE = fileURLToPath(
  new URL("../../../playwright/.auth/read-only.json", import.meta.url),
);

export const EMPTY_AUTH_STATE = { cookies: [], origins: [] };

const baseUser = {
  provider: "local",
  roles: { global: [], cluster: [], project: [] },
  enabled: true,
  mustChangePassword: false,
  lastLogin: "2026-01-01T00:00:00Z",
  createdAt: "2026-01-01T00:00:00Z",
};

export const adminAuthUser = {
  ...baseUser,
  id: "user-admin",
  username: "admin",
  email: "admin@example.com",
  displayName: "Admin User",
  globalRoles: ["admin"],
  isSuperuser: true,
};

export const readOnlyAuthUser = {
  ...baseUser,
  id: "u1",
  username: "reader",
  email: "reader@example.com",
  displayName: "Read Only",
  globalRoles: ["viewer"],
  isSuperuser: false,
  roles: {
    global: [
      {
        roleName: "reader",
        roleRules: [{ resource: "clusters", verbs: ["read", "list"] }],
      },
    ],
    cluster: [],
    project: [],
  },
};
