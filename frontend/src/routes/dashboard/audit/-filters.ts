import type { AuditLogQueryParams } from '@/lib/api';

export const PAGE_SIZE = 50;

export type AuditFilters = {
  q: string;
  actor: string;
  target: string;
  action: string;
  actionClass: string;
  result: string;
  clusterId: string;
  projectId: string;
  correlationId: string;
  requestId: string;
  from: string;
  to: string;
};

export const emptyFilters: AuditFilters = {
  q: '',
  actor: '',
  target: '',
  action: '',
  actionClass: 'all',
  result: 'all',
  clusterId: '',
  projectId: '',
  correlationId: '',
  requestId: '',
  from: '',
  to: '',
};

const ADVANCED_KEYS: (keyof AuditFilters)[] = [
  'actor',
  'target',
  'action',
  'clusterId',
  'projectId',
  'correlationId',
  'requestId',
  'from',
  'to',
];

export function isFilterActive(key: keyof AuditFilters, value: string): boolean {
  if (key === 'actionClass' || key === 'result') return value !== 'all';
  return Boolean(value.trim());
}

export function countActiveFilters(filters: AuditFilters): number {
  return (Object.keys(filters) as (keyof AuditFilters)[]).filter((key) =>
    isFilterActive(key, filters[key]),
  ).length;
}

export function countAdvancedFilters(filters: AuditFilters): number {
  return ADVANCED_KEYS.filter((key) => isFilterActive(key, filters[key])).length;
}

export function toRFC3339(value: string): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return undefined;
  return date.toISOString();
}

export function buildAuditQuery(filters: AuditFilters, page: number): AuditLogQueryParams {
  return {
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    q: filters.q.trim() || undefined,
    actor: filters.actor.trim() || undefined,
    target: filters.target.trim() || undefined,
    action: filters.action.trim() || undefined,
    action_class: filters.actionClass !== 'all' ? filters.actionClass : undefined,
    result: filters.result !== 'all' ? filters.result : undefined,
    cluster_id: filters.clusterId.trim() || undefined,
    project_id: filters.projectId.trim() || undefined,
    correlation_id: filters.correlationId.trim() || undefined,
    request_id: filters.requestId.trim() || undefined,
    from: toRFC3339(filters.from),
    to: toRFC3339(filters.to),
  };
}

export type AuditFilterChip = { key: keyof AuditFilters; label: string };

export function auditFilterChips(
  filters: AuditFilters,
  names?: { clusters?: Record<string, string>; projects?: Record<string, string> },
): AuditFilterChip[] {
  const chips: AuditFilterChip[] = [];
  const push = (key: keyof AuditFilters, label: string, value: string) => {
    if (!isFilterActive(key, value)) return;
    chips.push({ key, label: `${label}: ${value}` });
  };
  push('q', 'Search', filters.q.trim());
  if (filters.actionClass !== 'all') chips.push({ key: 'actionClass', label: `Class: ${filters.actionClass}` });
  if (filters.result !== 'all') chips.push({ key: 'result', label: `Result: ${filters.result}` });
  push('actor', 'Actor', filters.actor.trim());
  push('action', 'Action', filters.action.trim());
  push('target', 'Target', filters.target.trim());
  if (filters.clusterId) {
    chips.push({
      key: 'clusterId',
      label: `Cluster: ${names?.clusters?.[filters.clusterId] || filters.clusterId}`,
    });
  }
  if (filters.projectId) {
    chips.push({
      key: 'projectId',
      label: `Project: ${names?.projects?.[filters.projectId] || filters.projectId}`,
    });
  }
  push('correlationId', 'Correlation', filters.correlationId.trim());
  push('requestId', 'Request', filters.requestId.trim());
  push('from', 'From', filters.from);
  push('to', 'To', filters.to);
  return chips;
}

export function clearFilterValue(key: keyof AuditFilters): string {
  if (key === 'actionClass' || key === 'result') return 'all';
  return '';
}
