import {
  adminWebhookDelete,
  adminWebhookDeliveries,
  adminWebhookGet,
  adminWebhookRetryDelivery,
  adminWebhooksCreate,
  adminWebhooksList,
  adminWebhookTest,
  adminWebhookUpdate,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type { PaginatedResponse } from "@/types";
import type {
  WebhookDelivery as WebhookDeliveryWire,
  WebhookSubscription as WebhookSubscriptionWire,
  WebhookSubscriptionRequest,
} from "@/types/openapi.generated";

export type WebhookTemplate = "slack" | "pagerduty" | "generic";

const WEBHOOK_PRESET_TEMPLATES: Record<WebhookTemplate, string> = {
  slack:
    '{"text":"{{ .event_name }}: {{ .detail.message }}","blocks":[{"type":"section","text":{"type":"mrkdwn","text":"*{{ .event_name }}*\\n`{{ .resource_type }}/{{ .resource_id }}`"}}]}',
  pagerduty:
    '{"routing_key":"REPLACE_ME","event_action":"trigger","payload":{"summary":"{{ .event_name }} — {{ .detail.message }}","source":"astronomer","severity":"error"}}',
  generic: "",
};

export interface WebhookFilter {
  /** Event name globs dispatched to this subscription. */
  events: string[];
}

export interface WebhookSubscriptionView {
  id: string;
  name: string;
  url: string;
  template: WebhookTemplate;
  /** Shared HMAC secret sentinel. The plaintext is never returned. */
  secret: string;
  secretConfigured: boolean;
  enabled: boolean;
  filters: WebhookFilter;
  payloadTemplate: string;
  extraHeaders: Record<string, string>;
  maxRetries: number;
  timeoutSeconds: number;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export interface WebhookWriteRequest {
  name: string;
  url: string;
  template: WebhookTemplate;
  secret?: string;
  enabled: boolean;
  filters: WebhookFilter;
}

export type WebhookDeliveryStatus =
  "queued" | "delivered" | "failed" | "dropped";

export interface WebhookDeliveryView {
  id: string;
  eventType: string;
  eventId: string;
  status: WebhookDeliveryStatus;
  responseCode?: number;
  responseBody?: string;
  errorMessage?: string;
  attempts: number;
  payloadSize: number;
  deliveredAt?: string;
  nextAttemptAt?: string;
  createdAt: string;
}

export interface WebhookTestReceiptView {
  deliveryId: string;
  subscriptionId: string;
  queuedAt: string;
  message: string;
}

export interface WebhookRetryReceiptView {
  deliveryId: string;
  message: string;
}

export interface WebhookRequestOptions {
  signal?: AbortSignal;
}

function inferTemplate(payloadTemplate: string): WebhookTemplate {
  if (payloadTemplate === WEBHOOK_PRESET_TEMPLATES.slack) return "slack";
  if (payloadTemplate === WEBHOOK_PRESET_TEMPLATES.pagerduty) {
    return "pagerduty";
  }
  return "generic";
}

function mapWebhookSubscription(
  wire: WebhookSubscriptionWire,
): WebhookSubscriptionView {
  return {
    id: wire.id,
    name: wire.name,
    url: wire.url,
    template: inferTemplate(wire.payload_template),
    secret: wire.secret,
    secretConfigured: wire.secret_configured,
    enabled: wire.enabled,
    filters: { events: wire.event_filters },
    payloadTemplate: wire.payload_template,
    extraHeaders: wire.extra_headers,
    maxRetries: wire.max_retries,
    timeoutSeconds: wire.timeout_seconds,
    createdBy: wire.created_by,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function requireWebhookSubscription(
  wire: WebhookSubscriptionWire | undefined,
): WebhookSubscriptionView {
  if (!wire) throw new Error("Webhook response omitted subscription data");
  return mapWebhookSubscription(wire);
}

function mapWebhookWriteRequest(
  body: Partial<WebhookWriteRequest>,
): WebhookSubscriptionRequest {
  return {
    ...(body.name !== undefined ? { name: body.name } : {}),
    ...(body.url !== undefined ? { url: body.url } : {}),
    ...(body.secret !== undefined ? { secret: body.secret } : {}),
    ...(body.enabled !== undefined ? { enabled: body.enabled } : {}),
    ...(body.filters !== undefined
      ? { event_filters: body.filters.events }
      : {}),
    ...(body.template !== undefined
      ? { payload_template: WEBHOOK_PRESET_TEMPLATES[body.template] }
      : {}),
  };
}

function mapWebhookDelivery(wire: WebhookDeliveryWire): WebhookDeliveryView {
  const validStatus = new Set<WebhookDeliveryStatus>([
    "queued",
    "delivered",
    "failed",
    "dropped",
  ]);
  return {
    id: wire.id,
    eventType: wire.event_name,
    eventId: wire.event_id,
    status: validStatus.has(wire.status as WebhookDeliveryStatus)
      ? (wire.status as WebhookDeliveryStatus)
      : "failed",
    responseCode: wire.response_status || undefined,
    responseBody: wire.response_body || undefined,
    errorMessage: wire.last_error || undefined,
    attempts: wire.attempts,
    payloadSize: wire.payload_size,
    deliveredAt: wire.delivered_at ?? undefined,
    nextAttemptAt: wire.next_attempt_at ?? undefined,
    createdAt: wire.created_at,
  };
}

export async function listWebhooks(
  options: WebhookRequestOptions = {},
): Promise<WebhookSubscriptionView[]> {
  const response = await adminWebhooksList({ signal: options.signal });
  return (response.data?.items ?? []).map(mapWebhookSubscription);
}

export async function getWebhook(
  id: string,
  options: WebhookRequestOptions = {},
): Promise<WebhookSubscriptionView> {
  const response = await adminWebhookGet({
    path: { id },
    signal: options.signal,
  });
  return requireWebhookSubscription(response.data);
}

export async function createWebhook(
  body: WebhookWriteRequest,
  options: WebhookRequestOptions = {},
): Promise<WebhookSubscriptionView> {
  const response = await adminWebhooksCreate({
    body: mapWebhookWriteRequest(body),
    signal: options.signal,
  });
  return requireWebhookSubscription(response.data);
}

export async function updateWebhook(
  id: string,
  body: Partial<WebhookWriteRequest>,
  options: WebhookRequestOptions = {},
): Promise<WebhookSubscriptionView> {
  const response = await adminWebhookUpdate({
    path: { id },
    body: mapWebhookWriteRequest(body),
    signal: options.signal,
  });
  return requireWebhookSubscription(response.data);
}

export async function deleteWebhook(
  id: string,
  options: WebhookRequestOptions = {},
): Promise<void> {
  await adminWebhookDelete({ path: { id }, signal: options.signal });
}

export async function testWebhook(
  id: string,
  options: WebhookRequestOptions = {},
): Promise<WebhookTestReceiptView> {
  const response = await adminWebhookTest({
    path: { id },
    headerParams: idempotencyHeaderParams(),
    signal: options.signal,
  });
  const wire = response.data;
  if (!wire) {
    throw new Error("Webhook test response omitted queue receipt data");
  }
  return {
    deliveryId: wire.id,
    subscriptionId: id,
    queuedAt: wire.created_at,
    message: wire.status,
  };
}

export async function listWebhookDeliveries(
  id: string,
  params: { page?: number; page_size?: number } = {},
  options: WebhookRequestOptions = {},
): Promise<PaginatedResponse<WebhookDeliveryView>> {
  const page = Math.max(1, params.page ?? 1);
  const pageSize = Math.max(1, params.page_size ?? 50);
  const response = await adminWebhookDeliveries({
    path: { id },
    query: { limit: pageSize, offset: (page - 1) * pageSize },
    signal: options.signal,
  });
  const data = response.data;
  const total = data?.total ?? 0;
  const limit = data?.limit ?? pageSize;
  const offset = data?.offset ?? (page - 1) * pageSize;
  return {
    data: (data?.items ?? []).map(mapWebhookDelivery),
    total,
    count: total,
    page: Math.floor(offset / limit) + 1,
    pageSize: limit,
    totalPages: Math.max(1, Math.ceil(total / limit)),
  };
}

export async function retryWebhookDelivery(
  webhookId: string,
  deliveryId: string,
  options: WebhookRequestOptions = {},
): Promise<WebhookRetryReceiptView> {
  const response = await adminWebhookRetryDelivery({
    path: { id: webhookId, delivery_id: deliveryId },
    headerParams: idempotencyHeaderParams(),
    signal: options.signal,
  });
  const wire = response.data;
  if (!wire) {
    throw new Error("Webhook retry response omitted queue receipt data");
  }
  return {
    deliveryId: wire.id,
    message: wire.status,
  };
}
