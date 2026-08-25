import type { APIResponse } from "@/types";

/**
 * Unwrap a possibly-enveloped API payload. Single-object endpoints return
 * `{ data: T }` (the standard `APIResponse` envelope); some return the bare
 * `T`. Returns `value.data` when the envelope is present, otherwise the value
 * itself.
 */
export function unwrapData<T>(value: T | APIResponse<T> | null | undefined): T {
  if (
    value &&
    typeof value === "object" &&
    "data" in (value as Record<string, unknown>)
  ) {
    return (value as APIResponse<T>).data;
  }
  return value as T;
}

type APIErrorShape = {
  status?: number;
  code?: string;
  response?: {
    status?: number;
    data?: {
      error?: { code?: string; message?: string };
      code?: string;
      message?: string;
    };
  };
  message?: string;
};

export interface StructuredApiError extends Error {
  status?: number;
  code?: string;
  response?: APIErrorShape["response"];
}

/**
 * Return transport-normalized error metadata without inspecting localized or
 * upstream message text. Kubernetes proxy failures can carry their code in the
 * standard nested error envelope, while raw Kubernetes Status responses still
 * have an authoritative HTTP status.
 */
export function apiErrorStatus(err: unknown): number | undefined {
  if (!err || typeof err !== "object") return undefined;
  const obj = err as APIErrorShape;
  return obj.status ?? obj.response?.status;
}

export function apiErrorCode(err: unknown): string | undefined {
  if (!err || typeof err !== "object") return undefined;
  const obj = err as APIErrorShape;
  return obj.code ?? obj.response?.data?.error?.code ?? obj.response?.data?.code;
}

export function extractApiErrorMessage(err: unknown): string | null {
  if (!err) return null;
  if (typeof err === "string") return err;
  const obj = err as APIErrorShape;
  return (
    obj.response?.data?.error?.message ??
    obj.response?.data?.message ??
    obj.message ??
    null
  );
}
