import {
  adminEmailsList,
  adminSmtpGet,
  adminSmtpTest,
  adminSmtpUpdate,
} from "@/lib/api/generated/client";
import type { PaginatedResponse } from "@/types";

interface ItemsEnvelope<T> {
  items?: T[];
  total?: number;
  limit?: number;
  offset?: number;
}

function toPaginatedResponse<T>(
  envelope: ItemsEnvelope<T> | PaginatedResponse<T> | T[] | undefined,
  params?: { page?: number; page_size?: number },
): PaginatedResponse<T> {
  if (Array.isArray(envelope)) {
    const pageSize = params?.page_size ?? envelope.length;
    return {
      data: envelope,
      total: envelope.length,
      count: envelope.length,
      next: null,
      previous: null,
      page: params?.page ?? 1,
      pageSize,
      totalPages:
        pageSize > 0 ? Math.max(1, Math.ceil(envelope.length / pageSize)) : 1,
    };
  }

  const data = Array.isArray(
    (envelope as PaginatedResponse<T> | undefined)?.data,
  )
    ? (envelope as PaginatedResponse<T>).data
    : ((envelope as ItemsEnvelope<T> | undefined)?.items ?? []);
  const total =
    (envelope as ItemsEnvelope<T> | undefined)?.total ?? data.length;
  const limit =
    (envelope as ItemsEnvelope<T> | undefined)?.limit ??
    (envelope as PaginatedResponse<T> | undefined)?.pageSize ??
    params?.page_size ??
    data.length ??
    0;
  const offset = (envelope as ItemsEnvelope<T> | undefined)?.offset ?? 0;
  const page =
    (envelope as PaginatedResponse<T> | undefined)?.page ??
    params?.page ??
    (limit > 0 ? Math.floor(offset / limit) + 1 : 1);
  const totalPages =
    (envelope as PaginatedResponse<T> | undefined)?.totalPages ??
    (limit > 0 ? Math.max(1, Math.ceil(total / limit)) : 1);

  return {
    data,
    total,
    count: (envelope as PaginatedResponse<T> | undefined)?.count ?? total,
    next: (envelope as PaginatedResponse<T> | undefined)?.next ?? null,
    previous: (envelope as PaginatedResponse<T> | undefined)?.previous ?? null,
    page,
    pageSize: limit,
    totalPages,
  };
}
// ============================================================
// Types — SMTP
// ============================================================

export type SmtpAuth = "plain" | "login" | "cram-md5" | "none";
export type SmtpEncryption = "starttls" | "tls" | "none";

export interface SmtpConfig {
  host: string;
  port: number;
  username: string;
  /**
   * On reads the backend returns a sentinel ("__redacted__") rather than the
   * stored secret. On writes, sending the same sentinel preserves the
   * existing password; sending any other value rotates it.
   */
  password: string;
  fromAddress: string;
  fromName: string;
  authMechanism: SmtpAuth;
  encryption: SmtpEncryption;
  requireTls: boolean;
  timeoutSeconds: number;
  updatedAt?: string;
}

export const SMTP_REDACTED_SENTINEL = "__redacted__";

export interface SmtpTestRequest {
  to: string;
}

export interface SmtpTestResult {
  success: boolean;
  message: string;
  durationMs: number;
}

export type EmailStatus = "queued" | "sending" | "sent" | "failed" | "bounced";

export interface SentEmail {
  id: string;
  to: string;
  subject: string;
  template: string;
  status: EmailStatus;
  attempts: number;
  lastError?: string;
  sentAt?: string;
  createdAt: string;
}

function smtpConfigFromWire(
  wire: Awaited<ReturnType<typeof adminSmtpGet>>["data"],
): SmtpConfig {
  return {
    host: wire?.host ?? "",
    port: wire?.port ?? 587,
    username: wire?.username ?? "",
    password: wire?.password ?? "",
    fromAddress: wire?.from_address ?? "",
    fromName: wire?.from_name ?? "",
    authMechanism: wire?.auth_mechanism ?? "plain",
    encryption: wire?.encryption ?? "starttls",
    requireTls: wire?.require_tls ?? true,
    timeoutSeconds: wire?.timeout_seconds ?? 30,
    updatedAt: wire?.updated_at,
  };
}

function smtpConfigToWire(body: Partial<SmtpConfig>) {
  return {
    host: body.host,
    port: body.port,
    username: body.username,
    password:
      body.password === SMTP_REDACTED_SENTINEL ? undefined : body.password,
    from_address: body.fromAddress,
    from_name: body.fromName,
    auth_mechanism: body.authMechanism,
    encryption: body.encryption,
    require_tls: body.requireTls,
    timeout_seconds: body.timeoutSeconds,
  };
}
// ============================================================
// SMTP — API funcs
// ============================================================

export async function getSmtpConfig(): Promise<SmtpConfig> {
  const response = await adminSmtpGet();
  return smtpConfigFromWire(response.data);
}

export async function updateSmtpConfig(
  body: Partial<SmtpConfig>,
): Promise<SmtpConfig> {
  const response = await adminSmtpUpdate({ body: smtpConfigToWire(body) });
  return smtpConfigFromWire(response.data);
}

export async function testSmtpConfig(
  body: SmtpTestRequest,
): Promise<SmtpTestResult> {
  const response = await adminSmtpTest({ body: { recipient: body.to } });
  return {
    success: response.success === true,
    message: response.success
      ? `Test email sent to ${response.recipient ?? body.to}`
      : "SMTP test failed",
    durationMs: 0,
  };
}

export async function listSentEmails(params?: {
  page?: number;
  page_size?: number;
  signal?: AbortSignal;
}) {
  const pageSize = params?.page_size ?? 25;
  const page = params?.page ?? 1;
  const response = await adminEmailsList({
    query: {
      limit: pageSize,
      offset: Math.max(0, page - 1) * pageSize,
    },
    signal: params?.signal,
  });
  const envelope: ItemsEnvelope<SentEmail> = {
    items: (response.data?.items ?? []).map((email) => ({
      id: email.id ?? "",
      to: email.to_address ?? "",
      subject: email.subject ?? "",
      template: email.template ?? "",
      status: (email.status as EmailStatus | undefined) ?? "queued",
      attempts: email.attempts ?? 0,
      lastError: email.last_error,
      sentAt: email.sent_at ?? undefined,
      createdAt: email.created_at ?? "",
    })),
    total: response.data?.total,
    limit: response.data?.limit,
    offset: response.data?.offset,
  };
  return toPaginatedResponse(envelope, {
    page,
    page_size: pageSize,
  });
}
