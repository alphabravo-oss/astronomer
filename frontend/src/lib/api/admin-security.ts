/**
 * Admin security-diagnostics API client (F-05, contained slice):
 *   - GET /admin/key-status         — live encryption + JWT key counts, for
 *     confirming a keyrotate landed (see docs/secret-rotation-runbook.md).
 *   - GET /admin/shell-sessions      — superuser view of every active kubectl
 *     shell session across the fleet.
 *   - GET /admin/shell-sessions/{id}/commands — the audited command trail for
 *     one session (closes the loop on the kubectl-shell RCE surface).
 *
 * All endpoints are superuser-gated server-side. The shared axios interceptor
 * in ../api.ts camelizes snake_case response keys, so the types below are
 * camelCase even though the wire format is snake_case.
 *
 * Re-exported from ../api.ts via `export * from './api/admin-security'`.
 */

import api from '../api';

export interface KeyStatus {
  encryptionKeys: number;
  jwtKeys: number;
  asOf: string;
}

export async function getKeyStatus(): Promise<KeyStatus> {
  // Bare JSON body (not APIResponse-wrapped); interceptor camelizes keys.
  const res = await api.get<KeyStatus>('/admin/key-status/');
  return res.data;
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

export async function listShellSessions(): Promise<ShellSession[]> {
  const res = await api.get<{ data: ShellSession[] }>('/admin/shell-sessions/');
  return res.data.data ?? [];
}

export interface ShellSessionCommand {
  commandAt: string;
  commandLine: string;
}

export async function listShellSessionCommands(
  sessionId: string,
): Promise<ShellSessionCommand[]> {
  const res = await api.get<{ data: ShellSessionCommand[] }>(
    `/admin/shell-sessions/${sessionId}/commands/`,
  );
  return res.data.data ?? [];
}
