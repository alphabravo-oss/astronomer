import type { APIResponse } from '@/types';

/**
 * Unwrap a possibly-enveloped API payload. Single-object endpoints return
 * `{ data: T }` (the standard `APIResponse` envelope); some return the bare
 * `T`. Returns `value.data` when the envelope is present, otherwise the value
 * itself.
 */
export function unwrapData<T>(value: T | APIResponse<T> | null | undefined): T {
  if (value && typeof value === 'object' && 'data' in (value as Record<string, unknown>)) {
    return (value as APIResponse<T>).data;
  }
  return value as T;
}

type APIErrorShape = {
  response?: {
    data?: {
      error?: { message?: string };
      message?: string;
    };
  };
  message?: string;
};

export function extractApiErrorMessage(err: unknown): string | null {
  if (!err) return null;
  if (typeof err === 'string') return err;
  const obj = err as APIErrorShape;
  return obj.response?.data?.error?.message ?? obj.response?.data?.message ?? obj.message ?? null;
}
