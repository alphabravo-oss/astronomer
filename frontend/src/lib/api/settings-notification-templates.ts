import {
  adminNotificationTemplateGet,
  adminNotificationTemplatePreview,
  adminNotificationTemplateReset,
  adminNotificationTemplatesList,
  adminNotificationTemplateUpdate,
  adminNotificationTemplateVariables,
} from "@/lib/api/generated/client";
import type {
  NotificationTemplateDetail,
  NotificationTemplateListItem,
  NotificationTemplatePreviewRequest,
  NotificationTemplatePreviewResponse,
  NotificationTemplateUpsertRequest,
  NotificationTemplateVariable,
} from "@/types/openapi.generated";

export type NotificationTemplateUpsertBody = NotificationTemplateUpsertRequest;
export type NotificationTemplatePreviewBody =
  NotificationTemplatePreviewRequest;

export interface NotificationTemplateVariableView {
  name: string;
  description: string;
  required: boolean;
  example: string;
}

export interface NotificationTemplateListItemView {
  key: string;
  channel: "email" | "webhook";
  description: string;
  bodyFormat: "text" | "markdown" | "html" | "json";
  hasOverride: boolean;
  enabled: boolean;
  updatedAt?: string;
}

export interface NotificationTemplateDetailView {
  key: string;
  channel: "email" | "webhook";
  description: string;
  bodyFormat: "text" | "markdown" | "html" | "json";
  defaultSubject: string;
  defaultBody: string;
  subject: string;
  body: string;
  hasOverride: boolean;
  enabled: boolean;
  updatedAt?: string;
  updatedBy?: string;
  variables: NotificationTemplateVariableView[];
}

export interface NotificationTemplatePreviewResultView {
  subject: string;
  body: string;
}

export interface NotificationTemplateRequestOptions {
  signal?: AbortSignal;
}

function requireData<T>(data: T | undefined, operation: string): T {
  if (data === undefined) throw new Error(`${operation} response omitted data`);
  return data;
}

function mapVariable(
  wire: NotificationTemplateVariable,
): NotificationTemplateVariableView {
  return {
    name: wire.name,
    description: wire.description,
    required: wire.required,
    example: wire.example,
  };
}

function mapListItem(
  wire: NotificationTemplateListItem,
): NotificationTemplateListItemView {
  return {
    key: wire.key,
    channel: wire.channel,
    description: wire.description,
    bodyFormat: wire.body_format,
    hasOverride: wire.has_override,
    enabled: wire.enabled,
    updatedAt: wire.updated_at,
  };
}

function mapDetail(
  wire: NotificationTemplateDetail,
): NotificationTemplateDetailView {
  return {
    key: wire.key,
    channel: wire.channel,
    description: wire.description,
    bodyFormat: wire.body_format,
    defaultSubject: wire.default_subject,
    defaultBody: wire.default_body,
    subject: wire.subject,
    body: wire.body,
    hasOverride: wire.has_override,
    enabled: wire.enabled,
    updatedAt: wire.updated_at,
    updatedBy: wire.updated_by,
    variables: wire.variables.map(mapVariable),
  };
}

function mapPreview(
  wire: NotificationTemplatePreviewResponse,
): NotificationTemplatePreviewResultView {
  return { subject: wire.subject, body: wire.body };
}

export async function listNotificationTemplates(
  options: NotificationTemplateRequestOptions = {},
): Promise<NotificationTemplateListItemView[]> {
  const response = await adminNotificationTemplatesList({
    signal: options.signal,
  });
  return requireData(response.data, "List notification templates").items.map(
    mapListItem,
  );
}

export async function getNotificationTemplate(
  key: string,
  options: NotificationTemplateRequestOptions = {},
): Promise<NotificationTemplateDetailView> {
  const response = await adminNotificationTemplateGet({
    path: { key },
    signal: options.signal,
  });
  return mapDetail(requireData(response.data, "Get notification template"));
}

export async function updateNotificationTemplate(
  key: string,
  body: NotificationTemplateUpsertBody,
  options: NotificationTemplateRequestOptions = {},
): Promise<NotificationTemplateDetailView> {
  const response = await adminNotificationTemplateUpdate({
    path: { key },
    body,
    signal: options.signal,
  });
  return mapDetail(requireData(response.data, "Update notification template"));
}

export async function resetNotificationTemplate(
  key: string,
  options: NotificationTemplateRequestOptions = {},
): Promise<void> {
  await adminNotificationTemplateReset({
    path: { key },
    signal: options.signal,
  });
}

export async function previewNotificationTemplate(
  key: string,
  body: NotificationTemplatePreviewBody,
  options: NotificationTemplateRequestOptions = {},
): Promise<NotificationTemplatePreviewResultView> {
  const response = await adminNotificationTemplatePreview({
    path: { key },
    body,
    signal: options.signal,
  });
  return mapPreview(
    requireData(response.data, "Preview notification template"),
  );
}

export async function getNotificationTemplateVariables(
  key: string,
  options: NotificationTemplateRequestOptions = {},
): Promise<NotificationTemplateVariableView[]> {
  const response = await adminNotificationTemplateVariables({
    path: { key },
    signal: options.signal,
  });
  return requireData(
    response.data,
    "Get notification template variables",
  ).variables.map(mapVariable);
}
