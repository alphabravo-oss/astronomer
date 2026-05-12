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

export interface ShellSession {
  id: string;
  cluster_id: string;
  user_id: string;
  status: 'starting' | 'active' | 'closed' | 'expired' | 'failed';
  pod_name: string;
  pod_namespace: string;
  container: string;
  started_at: string;
  last_input_at: string;
  expires_at: string;
  idle_timeout_seconds: number;
  command_count?: number;
}

export interface RecordedCommand {
  command_at: string;
  command_line: string;
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
