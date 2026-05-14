// Operations admin tab — client for /api/v1/admin/queues/*.
//
// Backend: internal/handler/admin_queues.go. Superuser-gated; the page's
// own auth gate fans the 403s into a friendlier "you need admin" notice.

import api from '../api';

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
