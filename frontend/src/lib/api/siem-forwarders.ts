import {
  adminSIEMForwarderDelete,
  adminSIEMForwarderGet,
  adminSIEMForwardersCreate,
  adminSIEMForwardersList,
  adminSIEMForwarderStatus,
  adminSIEMForwarderTest,
  adminSIEMForwarderUpdate,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type { SIEMForwarder, SIEMForwarderStatus } from "@/types";
import type {
  SIEMForwarderRequest,
  SIEMForwarderResponse,
  SIEMForwarderStatusResponse,
  SIEMForwarderTestReceipt,
} from "@/types/openapi.generated";

export const SIEM_AUTH_SENTINEL = "<encrypted>";
export type SIEMForwarderWriteRequest = Omit<
  SIEMForwarderRequest,
  "transport" | "format"
> & {
  transport?: string;
  format?: string;
};

export interface SIEMTestReceiptView {
  queueId: string;
  forwarderId: string;
  queuedAt: string;
  message: string;
}

export interface SIEMRequestOptions {
  signal?: AbortSignal;
}

function requireData<T>(data: T | undefined, operation: string): T {
  if (data === undefined) throw new Error(`${operation} response omitted data`);
  return data;
}

function mapWriteRequest(
  body: SIEMForwarderWriteRequest,
): SIEMForwarderRequest {
  const transports = [
    "syslog_udp",
    "syslog_tcp",
    "syslog_tls",
    "splunk_hec",
    "ndjson_https",
  ] as const;
  const formats = ["", "rfc5424", "rfc3164", "cef", "ndjson"] as const;
  if (
    body.transport &&
    !transports.includes(body.transport as (typeof transports)[number])
  ) {
    throw new Error(`Unsupported SIEM transport: ${body.transport}`);
  }
  if (
    body.format !== undefined &&
    !formats.includes(body.format as (typeof formats)[number])
  ) {
    throw new Error(`Unsupported SIEM format: ${body.format}`);
  }
  return body as SIEMForwarderRequest;
}

function mapForwarder(wire: SIEMForwarderResponse): SIEMForwarder {
  return {
    id: wire.id,
    name: wire.name,
    transport: wire.transport,
    endpoint: wire.endpoint,
    auth: wire.auth,
    authConfigured: wire.auth_configured,
    eventFilters: wire.event_filters,
    format: wire.format,
    tlsSkipVerify: wire.tls_skip_verify,
    caCertConfigured: wire.ca_cert_configured,
    batchSize: wire.batch_size,
    flushIntervalMs: wire.flush_interval_ms,
    timeoutSeconds: wire.timeout_seconds,
    enabled: wire.enabled,
    createdBy: wire.created_by,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function mapStatus(wire: SIEMForwarderStatusResponse): SIEMForwarderStatus {
  return {
    forwarderId: wire.forwarder_id,
    lastSentAt: wire.last_sent_at,
    lastError: wire.last_error,
    queueDepth: wire.queue_depth,
    droppedTotal: wire.dropped_total,
    dispatchedTotal: wire.dispatched_total,
    updatedAt: wire.updated_at,
  };
}

function mapTestReceipt(wire: SIEMForwarderTestReceipt): SIEMTestReceiptView {
  return {
    queueId: wire.operation_id,
    forwarderId: wire.forwarder_id,
    queuedAt: wire.created_at,
    message: wire.status,
  };
}

export async function listSIEMForwarders(
  options: SIEMRequestOptions = {},
): Promise<SIEMForwarder[]> {
  const response = await adminSIEMForwardersList({ signal: options.signal });
  return requireData(response.data, "List SIEM forwarders").items.map(
    mapForwarder,
  );
}

export async function getSIEMForwarder(
  id: string,
  options: SIEMRequestOptions = {},
): Promise<SIEMForwarder> {
  const response = await adminSIEMForwarderGet({
    path: { id },
    signal: options.signal,
  });
  return mapForwarder(requireData(response.data, "Get SIEM forwarder"));
}

export async function createSIEMForwarder(
  body: SIEMForwarderWriteRequest,
  options: SIEMRequestOptions = {},
): Promise<SIEMForwarder> {
  const response = await adminSIEMForwardersCreate({
    body: mapWriteRequest(body),
    signal: options.signal,
  });
  return mapForwarder(requireData(response.data, "Create SIEM forwarder"));
}

export async function updateSIEMForwarder(
  id: string,
  body: SIEMForwarderWriteRequest,
  options: SIEMRequestOptions = {},
): Promise<SIEMForwarder> {
  const response = await adminSIEMForwarderUpdate({
    path: { id },
    body: mapWriteRequest(body),
    signal: options.signal,
  });
  return mapForwarder(requireData(response.data, "Update SIEM forwarder"));
}

export async function deleteSIEMForwarder(
  id: string,
  options: SIEMRequestOptions = {},
): Promise<void> {
  await adminSIEMForwarderDelete({ path: { id }, signal: options.signal });
}

export async function testSIEMForwarder(
  id: string,
  options: SIEMRequestOptions = {},
): Promise<SIEMTestReceiptView> {
  const response = await adminSIEMForwarderTest({
    path: { id },
    headerParams: idempotencyHeaderParams(),
    signal: options.signal,
  });
  return mapTestReceipt(requireData(response.data, "Test SIEM forwarder"));
}

export async function getSIEMForwarderStatus(
  id: string,
  options: SIEMRequestOptions = {},
): Promise<SIEMForwarderStatus> {
  const response = await adminSIEMForwarderStatus({
    path: { id },
    signal: options.signal,
  });
  return mapStatus(response);
}
