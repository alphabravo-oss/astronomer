/**
 * SCIM provisioning-token admin API client (F-05).
 *
 * Backend: /api/v1/admin/scim-tokens/* (mint / list / revoke). Superuser-gated
 * server-side. The plaintext token is returned ONLY in the create response
 * under `token`; list rows carry metadata only.
 *
 * Re-exported from ../api.ts via `export * from './api/scim-tokens'`.
 */
import {
  deleteAdminScimTokensById,
  getAdminScimTokens,
  postAdminScimTokens,
} from "@/lib/api/generated/client";
import type { SCIMToken, SCIMTokenCreated } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type SCIMTokenWire = OpenAPIComponents["schemas"]["SCIMToken"];

function mapSCIMToken(token: SCIMTokenWire): SCIMToken {
  return {
    id: token.id,
    name: token.name,
    prefix: token.prefix,
    lastUsedAt: token.last_used_at,
    createdAt: token.created_at,
  };
}

export async function listSCIMTokens(): Promise<SCIMToken[]> {
  const response = await getAdminScimTokens();
  return response.data.tokens.map(mapSCIMToken);
}

export async function createSCIMToken(name: string): Promise<SCIMTokenCreated> {
  const response = await postAdminScimTokens({ body: { name } });
  return { ...mapSCIMToken(response.data), token: response.data.token };
}

export async function deleteSCIMToken(id: string): Promise<void> {
  await deleteAdminScimTokensById({ path: { id } });
}
