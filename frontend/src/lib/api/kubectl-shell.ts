// Migration 065 / sprint 17 — in-browser kubectl shell API client.

import {
  getClustersByClusterIdShellSessionsById,
  getClustersByClusterIdShellSessionsByIdCommands,
  getClustersByIdShellSessions,
  postClustersByIdShellSessions,
  postClustersByIdShellSessionsBySessionIdClose,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type ShellSessionWire = OpenAPIComponents["schemas"]["KubectlSession"];
type RecordedCommandWire =
  OpenAPIComponents["schemas"]["KubectlRecordedCommand"];

export interface ShellSession {
  id: string;
  clusterId: string;
  userId: string;
  status: "starting" | "active" | "closed" | "expired" | "failed";
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

export interface ShellCloseReceipt {
  status: "closed";
}

export interface ShellRequestOptions {
  signal?: AbortSignal;
}

export interface ShellCommandListOptions extends ShellRequestOptions {
  limit?: number;
  offset?: number;
}

function shellSessionFromWire(wire: ShellSessionWire): ShellSession {
  return {
    id: wire.id,
    clusterId: wire.cluster_id,
    userId: wire.user_id,
    status: wire.status,
    podName: wire.pod_name,
    podNamespace: wire.pod_namespace,
    container: wire.container,
    startedAt: wire.started_at,
    lastInputAt: wire.last_input_at,
    expiresAt: wire.expires_at,
    idleTimeoutSeconds: wire.idle_timeout_seconds,
    ...(wire.command_count === undefined
      ? {}
      : { commandCount: wire.command_count }),
  };
}

function recordedCommandFromWire(wire: RecordedCommandWire): RecordedCommand {
  return {
    commandAt: wire.command_at,
    commandLine: wire.command_line,
  };
}

export async function openShellSession(
  clusterId: string,
  options: ShellRequestOptions = {},
): Promise<ShellSession> {
  const response = await postClustersByIdShellSessions({
    path: { id: clusterId },
    signal: options.signal,
  });
  return shellSessionFromWire(response.data);
}

export async function getShellSession(
  clusterId: string,
  sessionId: string,
  options: ShellRequestOptions = {},
): Promise<ShellSession> {
  const response = await getClustersByClusterIdShellSessionsById({
    path: { cluster_id: clusterId, id: sessionId },
    signal: options.signal,
  });
  return shellSessionFromWire(response.data);
}

export async function listShellSessions(
  clusterId: string,
  options: ShellRequestOptions = {},
): Promise<ShellSession[]> {
  const response = await getClustersByIdShellSessions({
    path: { id: clusterId },
    signal: options.signal,
  });
  return response.data.map(shellSessionFromWire);
}

export async function closeShellSession(
  clusterId: string,
  sessionId: string,
  options: ShellRequestOptions = {},
): Promise<ShellCloseReceipt> {
  const response = await postClustersByIdShellSessionsBySessionIdClose({
    path: { id: clusterId, session_id: sessionId },
    signal: options.signal,
  });
  return { status: response.data.status };
}

export async function listShellSessionCommands(
  clusterId: string,
  sessionId: string,
  options: ShellCommandListOptions = {},
): Promise<RecordedCommand[]> {
  const response = await getClustersByClusterIdShellSessionsByIdCommands({
    path: { cluster_id: clusterId, id: sessionId },
    query: { limit: options.limit, offset: options.offset },
    signal: options.signal,
  });
  return response.data.map(recordedCommandFromWire);
}
