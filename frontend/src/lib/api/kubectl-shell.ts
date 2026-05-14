// Migration 065 / sprint 17 — in-browser kubectl shell API client.
//
// Pairs with internal/handler/kubectl_shell.go. The session lifecycle is:
//   1. POST    sessions/            → creates a session, returns SessionInfo
//   2. WS      sessions/{id}/        → opens the WebSocket (server 307-redirects
//                                       onto /api/v1/ws/exec/...)
//   3. POST    sessions/{id}/close/  → tears down the in-cluster pod
//   4. GET     sessions/{id}/commands/ → audit drill-down (operator's own
//                                       recorded command lines)
//
// All endpoints are gated on clusters:update.

import api from '../api';

// camelCase to match the global axios response interceptor (frontend/src/lib/api.ts)
// which transforms every snake_case key into camelCase before the
// caller sees it. Until that interceptor was added this interface
// used snake_case, which made every `session.cluster_id` read return
// undefined at runtime — the WS URL ended up
// `…/clusters/undefined/shell/…` and the terminal age/expires copy
// rendered Invalid Date. Both bugs traced back to this mismatch.
export interface ShellSession {
  id: string;
  clusterId: string;
  userId: string;
  status: 'starting' | 'active' | 'closed' | 'expired' | 'failed';
  podName: string;
  podNamespace: string;
  container: string;
  startedAt: string;
  lastInputAt: string;
  expiresAt: string;
  idleTimeoutSeconds: number;
  commandCount?: number;
}

export interface RecordedCommand {
  commandAt: string;
  commandLine: string;
}

export async function openShellSession(clusterId: string): Promise<ShellSession> {
  const resp = await api.post<{ data: ShellSession }>(
    `/clusters/${clusterId}/shell/sessions/`,
    {}
  );
  return resp.data.data;
}

export async function getShellSession(
  clusterId: string,
  sessionId: string,
): Promise<ShellSession> {
  const resp = await api.get<{ data: ShellSession }>(
    `/clusters/${clusterId}/shell/sessions/${sessionId}/`,
  );
  return resp.data.data;
}

export async function listShellSessions(clusterId: string): Promise<ShellSession[]> {
  const resp = await api.get<{ data: ShellSession[] }>(
    `/clusters/${clusterId}/shell/sessions/`,
  );
  return resp.data.data ?? [];
}

export async function closeShellSession(
  clusterId: string,
  sessionId: string,
): Promise<void> {
  await api.post(`/clusters/${clusterId}/shell/sessions/${sessionId}/close/`, {});
}

export async function listShellSessionCommands(
  clusterId: string,
  sessionId: string,
): Promise<RecordedCommand[]> {
  const resp = await api.get<{ data: RecordedCommand[] }>(
    `/clusters/${clusterId}/shell/sessions/${sessionId}/commands/`,
  );
  return resp.data.data ?? [];
}
