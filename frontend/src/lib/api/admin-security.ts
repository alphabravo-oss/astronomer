/**
 * Admin security-diagnostics API client (F-05, contained slice):
 *   - GET /admin/key-status         — live encryption + JWT key counts, for
 *     confirming a keyrotate landed (see docs/secret-rotation-runbook.md).
 *   - GET /admin/shell-sessions      — superuser view of every active kubectl
 *     shell session across managed clusters.
 *   - GET /admin/shell-sessions/{id}/commands — the audited command trail for
 *     one session (closes the loop on the kubectl-shell RCE surface).
 *
 * All endpoints are superuser-gated server-side. Generated responses retain
 * snake_case and the functions below map them into camelCase view models.
 *
 * Re-exported from ../api.ts via `export * from './api/admin-security'`.
 */

import {
  adminKeyStatus,
  adminShellSessionCommands,
  adminShellSessionsList,
} from "@/lib/api/generated/client";

export interface KeyStatus {
  encryptionKeys: number;
  jwtKeys: number;
  /**
   * Credentials still set to a value published in the Astronomer repository
   * ("secret_key" | "encryption_key"). Non-empty means this install's tokens
   * are forgeable and its stored credentials are readable — see
   * docs/runbooks/insecure-dev-key-in-use.md. Optional: older servers omit it.
   */
  insecureDevKeys?: string[];
  asOf: string;
}

export async function getKeyStatus(signal?: AbortSignal): Promise<KeyStatus> {
  const response = await adminKeyStatus({ signal });
  return {
    encryptionKeys: response.encryption_keys,
    jwtKeys: response.jwt_keys,
    insecureDevKeys: response.insecure_dev_keys,
    asOf: response.as_of,
  };
}

export interface ShellSession {
  id: string;
  clusterId: string;
  userId: string;
  status: string;
  podName: string;
  podNamespace: string;
  container: string;
  startedAt: string;
  lastInputAt: string;
  expiresAt: string;
  idleTimeoutSeconds: number;
  commandCount?: number;
}

export async function listShellSessions(signal?: AbortSignal): Promise<ShellSession[]> {
  const response = await adminShellSessionsList({ signal });
  return (response.data ?? []).map((session) => ({
    id: session.id ?? "",
    clusterId: session.cluster_id ?? "",
    userId: session.user_id ?? "",
    status: session.status ?? "unknown",
    podName: session.pod_name ?? "",
    podNamespace: session.pod_namespace ?? "",
    container: session.container ?? "",
    startedAt: session.started_at ?? "",
    lastInputAt: session.last_input_at ?? "",
    expiresAt: session.expires_at ?? "",
    idleTimeoutSeconds: session.idle_timeout_seconds ?? 0,
    commandCount: session.command_count,
  }));
}

export interface ShellSessionCommand {
  commandAt: string;
  commandLine: string;
}

export async function listShellSessionCommands(
  sessionId: string,
  signal?: AbortSignal,
): Promise<ShellSessionCommand[]> {
  const response = await adminShellSessionCommands({
    path: { id: sessionId },
    signal,
  });
  const commands = (response.data ?? []) as Array<{
    command_at?: string;
    command_line?: string;
  }>;
  return commands.map((command) => ({
    commandAt: command.command_at ?? "",
    commandLine: command.command_line ?? "",
  }));
}
