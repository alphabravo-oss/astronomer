// Operations admin tab — client for /api/v1/admin/queues/*.
//
// Backend: internal/handler/admin_queues.go. Superuser-gated; the page's
// own auth gate fans the 403s into a friendlier "you need admin" notice.

import api from '../api';
import type { APIResponse, PaginatedResponse } from '@/types';

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

export type TaskOutboxStatus = 'pending' | 'delivering' | 'failed' | 'delivered' | 'dead';

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

export async function listQueues(): Promise<QueueSummary[]> {
  const res = await api.get<QueueSummary[]>('/admin/queues/');
  return res.data ?? [];
}

export async function listDLQ(queue: string): Promise<{ queue: string; dlq: DLQEntry[]; count: number }> {
  const res = await api.get<{ queue: string; dlq: DLQEntry[]; count: number }>(`/admin/queues/${encodeURIComponent(queue)}/dlq/`);
  return res.data ?? { queue, dlq: [], count: 0 };
}

export async function retryDLQTask(queue: string, id: string): Promise<void> {
  await api.post(`/admin/queues/${encodeURIComponent(queue)}/dlq/${encodeURIComponent(id)}/retry/`);
}

export async function discardDLQTask(queue: string, id: string): Promise<void> {
  await api.delete(`/admin/queues/${encodeURIComponent(queue)}/dlq/${encodeURIComponent(id)}/`);
}

export async function listTaskOutbox(status: TaskOutboxStatus | '' = 'dead'): Promise<PaginatedResponse<TaskOutboxEntry>> {
  const res = await api.get<PaginatedResponse<TaskOutboxEntry>>('/admin/task-outbox/', {
    params: { status, limit: 100 },
  });
  return res.data;
}

export async function retryTaskOutbox(id: string): Promise<TaskOutboxEntry> {
  const res = await api.post<APIResponse<TaskOutboxEntry>>(`/admin/task-outbox/${encodeURIComponent(id)}/retry/`);
  return res.data.data;
}
