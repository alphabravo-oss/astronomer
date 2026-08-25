// Operations admin tab — client for /api/v1/admin/queues/*.
//
// Backend: internal/handler/admin_queues.go. Superuser-gated; the page's
// own auth gate fans the 403s into a friendlier "you need admin" notice.

import {
  deleteAdminQueuesByQueueDlqById,
  getAdminQueues,
  getAdminQueuesByQueueDlq,
  getAdminQueuesOperationsById,
  getAdminTaskOutbox,
  postAdminTaskOutboxByIdRetry,
  postAdminQueuesByQueueDlqByIdRetry,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type { PaginatedResponse } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export interface QueueSummary {
  name: string;
  size: number;
  active: number;
  pending: number;
  scheduled: number;
  retry: number;
  archived: number;
  completed: number;
  paused: boolean;
  as_of: string;
}

export interface DLQEntry {
  id: string;
  type: string;
  retried: number;
  last_err: string;
  last_failed_at: string;
}

export type TaskOutboxStatus =
  "pending" | "delivering" | "failed" | "delivered" | "dead";

export interface TaskOutboxEntry {
  id: string;
  dedupe_key?: string;
  task_type: string;
  queue_name: string;
  max_retry: number;
  timeout_seconds: number;
  unique_seconds: number;
  max_delivery_attempts: number;
  status: TaskOutboxStatus;
  attempt_count: number;
  next_attempt_at?: string;
  locked_until?: string;
  delivered_at?: string;
  last_error?: string;
  payload_size: number;
  created_at?: string;
  updated_at?: string;
}

export async function listQueues(signal?: AbortSignal): Promise<QueueSummary[]> {
  const response = await getAdminQueues({ signal });
  const data = response.data as QueueSummary[] | undefined;
  return Array.isArray(data) ? data : [];
}

export async function listDLQ(
  queue: string,
  signal?: AbortSignal,
): Promise<{ queue: string; dlq: DLQEntry[]; count: number }> {
  const fallback = { queue, dlq: [], count: 0 };
  const response = await getAdminQueuesByQueueDlq({ path: { queue }, signal });
  return (response.data as typeof fallback | undefined) ?? fallback;
}

export type AdminQueueOperation =
  OpenAPIComponents["schemas"]["AdminQueueOperation"];

export async function retryDLQTask(
  queue: string,
  id: string,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<AdminQueueOperation> {
  const response = await postAdminQueuesByQueueDlqByIdRetry({
    path: { queue, id },
    headerParams: { "Idempotency-Key": options.idempotencyKey },
    signal: options.signal,
  });
  return response.data;
}

export async function discardDLQTask(
  queue: string,
  id: string,
  options: { idempotencyKey: string; signal?: AbortSignal },
): Promise<AdminQueueOperation> {
  const response = await deleteAdminQueuesByQueueDlqById({
    path: { queue, id },
    headerParams: { "Idempotency-Key": options.idempotencyKey },
    signal: options.signal,
  });
  return response.data;
}

export async function getDLQOperation(
  id: string,
  signal?: AbortSignal,
): Promise<AdminQueueOperation> {
  const response = await getAdminQueuesOperationsById({
    path: { id },
    signal,
  });
  return response.data;
}

export async function listTaskOutbox(
  status: TaskOutboxStatus | "" = "dead",
  signal?: AbortSignal,
): Promise<PaginatedResponse<TaskOutboxEntry>> {
  const response = await getAdminTaskOutbox({
    query: { status: status || undefined, limit: 100 },
    signal,
  });
  const count = response.count ?? response.data?.length ?? 0;
  return {
    data: (response.data ?? []) as TaskOutboxEntry[],
    total: count,
    count,
    next: response.next ?? null,
    previous: response.previous ?? null,
    page: 1,
    pageSize: 100,
    totalPages: Math.max(1, Math.ceil(count / 100)),
  };
}

export async function retryTaskOutbox(
  id: string,
  signal?: AbortSignal,
): Promise<TaskOutboxEntry> {
  const response = await postAdminTaskOutboxByIdRetry({
    path: { id },
    headerParams: idempotencyHeaderParams(),
    signal,
  });
  return response.data as TaskOutboxEntry;
}
