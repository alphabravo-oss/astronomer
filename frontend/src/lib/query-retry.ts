import { apiErrorStatus } from "@/lib/api/errors";

const MAX_QUERY_RETRIES = 2;

/**
 * Retry transient query failures only. Client errors are deterministic except
 * for request timeouts and rate limiting, so replaying them wastes capacity and
 * can amplify authentication refresh storms.
 */
export function shouldRetryQuery(
  failureCount: number,
  error: unknown,
): boolean {
  if (failureCount >= MAX_QUERY_RETRIES) return false;

  const status = apiErrorStatus(error);
  if (status == null) return true;
  if (status === 408 || status === 429) return true;
  return status < 400 || status >= 500;
}

/**
 * Route error boundaries own unexpected server failures. A 503 is a normal,
 * recoverable state while an adopted cluster's agent reconnects, so keep it in
 * the query result for the affected panel to render without replacing the
 * entire route.
 */
export function shouldThrowQueryError(error: unknown): boolean {
  const status = apiErrorStatus(error);
  return status != null && status >= 500 && status !== 503;
}
