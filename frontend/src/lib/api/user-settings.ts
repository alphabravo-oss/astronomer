import {
  deleteSettingsTokensById,
  deleteUsersById,
  getSettingsGeneral,
  getSettingsSso,
  getSettingsTokens,
  getUsers as getUsersOperation,
  postSettingsSso,
  postSettingsTokens,
  postUsers,
  postUsersByIdResetPassword,
  putSettingsGeneral,
  putUsersById,
} from "@/lib/api/generated/client";
import type { APIToken, PaginatedResponse, SSOProvider, User } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];
type UserWire = Schemas["UsersSettingsUserListItem"];
type TokenWire = Schemas["UsersSettingsTokenListItem"];
type CreatedTokenWire = Schemas["UsersSettingsCreatedToken"];
type SSOProviderWire = Schemas["UsersSettingsSSOProvider"];

export type GeneralSettings = Required<Schemas["UsersSettingsGeneral"]>;

export interface UserListParameters {
  page?: number;
  pageSize?: number;
}

export interface CreateUserInput {
  username: string;
  email: string;
  displayName: string;
  password: string;
}

export interface UpdateUserInput {
  displayName?: string;
  email?: string;
  enabled?: boolean;
}

export interface CreateSSOProviderInput {
  type: "github" | "google" | "oidc";
  name: string;
  enabled: boolean;
  config: {
    clientId?: string;
    clientSecret?: string;
    metadataUrl?: string;
    allowedOrganizations?: string;
    autoCreateUsers?: boolean;
  };
}

export interface CreateAPITokenInput {
  name: string;
  expiresInDays?: number;
  scopes?: string[];
  allowedCidrs?: string;
}

export type CreatedAPIToken = APIToken & { token: string };

function requiredString(value: string | undefined, field: string): string {
  if (!value) throw new Error(`User/settings API response omitted ${field}`);
  return value;
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function splitDisplayName(value: string | undefined): {
  first_name?: string;
  last_name?: string;
} {
  const parts = value?.trim().split(/\s+/).filter(Boolean) ?? [];
  if (parts.length === 0) return {};
  return { first_name: parts[0], last_name: parts.slice(1).join(" ") };
}

/** Explicit generated-wire mapper; user administration never uses interceptor camelization. */
export function mapUser(wire: UserWire): User {
  const provider = ["local", "github", "google", "oidc", "saml"].includes(
    wire.provider ?? "",
  )
    ? (wire.provider as User["provider"])
    : "local";
  return {
    id: requiredString(wire.id, "user.id"),
    username: requiredString(wire.username, "user.username"),
    email: requiredString(wire.email, "user.email"),
    displayName: wire.displayName || wire.username || wire.email || "User",
    provider,
    globalRoles: wire.globalRoles ?? [],
    isSuperuser: wire.is_superuser ?? false,
    is_superuser: wire.is_superuser ?? false,
    enabled: wire.enabled ?? false,
    lastLogin: wire.lastLogin ?? "",
    createdAt: wire.createdAt ?? "",
  };
}

function mapSSOProvider(wire: SSOProviderWire): SSOProvider {
  const type = wire.type;
  if (type !== "github" && type !== "google" && type !== "oidc") {
    throw new Error(
      `SSO provider returned unsupported type: ${type ?? "missing"}`,
    );
  }
  return {
    id: requiredString(wire.id, "sso_provider.id"),
    provider: wire.provider ?? type,
    type,
    name: requiredString(wire.name, "sso_provider.name"),
    enabled: wire.enabled ?? false,
    config: wire.config ?? {},
    createdAt: wire.created_at ?? "",
    updatedAt: wire.updated_at ?? "",
  };
}

function mapToken(wire: TokenWire | CreatedTokenWire): APIToken {
  return {
    id: requiredString(wire.id, "token.id"),
    name: requiredString(wire.name, "token.name"),
    prefix: requiredString(wire.prefix, "token.prefix"),
    expiresAt: wire.expires_at ?? undefined,
    lastUsedAt:
      "last_used_at" in wire ? (wire.last_used_at ?? undefined) : undefined,
    isRevoked: "is_revoked" in wire ? (wire.is_revoked ?? false) : false,
    createdAt: wire.created_at ?? "",
    scopes: wire.scopes ?? [],
    allowedCidrs: wire.allowed_cidrs || undefined,
    lastSeenRemoteIp:
      "last_seen_remote_ip" in wire
        ? wire.last_seen_remote_ip || undefined
        : undefined,
  };
}

export async function getUsers(
  params?: UserListParameters,
): Promise<PaginatedResponse<User>> {
  const pageSize = Math.max(1, Math.min(200, params?.pageSize ?? 20));
  const page = Math.max(1, params?.page ?? 1);
  const response = await getUsersOperation({
    query: { limit: pageSize, offset: (page - 1) * pageSize },
  });
  const count = response.count;
  return {
    data: (response.data ?? []).map(mapUser),
    total: count,
    count,
    next: response.next,
    previous: response.previous,
    page,
    pageSize,
    totalPages: Math.max(1, Math.ceil(count / pageSize)),
  };
}

export async function createUser(input: CreateUserInput): Promise<User> {
  return mapUser(
    requireData(
      await postUsers({
        body: {
          email: input.email,
          username: input.username,
          password: input.password,
          ...splitDisplayName(input.displayName),
        },
      }),
      "createUser",
    ),
  );
}

export async function updateUser(
  id: string,
  input: UpdateUserInput,
): Promise<User> {
  return mapUser(
    requireData(
      await putUsersById({
        path: { id },
        body: {
          email: input.email,
          is_active: input.enabled,
          ...splitDisplayName(input.displayName),
        },
      }),
      "updateUser",
    ),
  );
}

export async function deleteUser(id: string): Promise<void> {
  await deleteUsersById({ path: { id } });
}

export async function resetUserPassword(
  id: string,
): Promise<{ temporaryPassword: string }> {
  const result = requireData(
    await postUsersByIdResetPassword({ path: { id } }),
    "resetUserPassword",
  );
  return {
    temporaryPassword: requiredString(
      result.temporary_password,
      "reset_password.temporary_password",
    ),
  };
}

export async function getGeneralSettings(): Promise<GeneralSettings> {
  return requireData(
    await getSettingsGeneral(),
    "getGeneralSettings",
  ) as GeneralSettings;
}

export async function saveGeneralSettings(
  data: GeneralSettings,
): Promise<GeneralSettings> {
  return requireData(
    await putSettingsGeneral({ body: data }),
    "saveGeneralSettings",
  ) as GeneralSettings;
}

export async function getSSOProviders(): Promise<SSOProvider[]> {
  const response = await getSettingsSso();
  const rows = Array.isArray(response)
    ? response
    : ((response as { data?: SSOProviderWire[] }).data ?? []);
  return rows.map(mapSSOProvider);
}

export async function createSSOProvider(
  input: CreateSSOProviderInput,
): Promise<SSOProvider> {
  return mapSSOProvider(
    requireData(
      await postSettingsSso({
        body: {
          type: input.type,
          name: input.name,
          enabled: input.enabled,
          config: {
            client_id: input.config.clientId ?? "",
            client_secret: input.config.clientSecret ?? "",
            metadata_url: input.config.metadataUrl,
            allowed_organizations: input.config.allowedOrganizations,
            auto_create_users: input.config.autoCreateUsers,
          },
        },
      }),
      "createSSOProvider",
    ),
  );
}

export async function getAPITokens(): Promise<APIToken[]> {
  const response = await getSettingsTokens({
    query: { limit: 200, offset: 0 },
  });
  return (response.data ?? []).map(mapToken);
}

export async function createAPIToken(
  input: CreateAPITokenInput,
): Promise<CreatedAPIToken> {
  const wire = requireData(
    await postSettingsTokens({
      body: {
        name: input.name,
        expires_in_days: input.expiresInDays,
        scopes: input.scopes,
        allowed_cidrs: input.allowedCidrs,
      },
    }),
    "createAPIToken",
  );
  return {
    ...mapToken(wire),
    token: requiredString(wire.token, "token.token"),
  };
}

export async function deleteAPIToken(id: string): Promise<void> {
  await deleteSettingsTokensById({ path: { id } });
}
