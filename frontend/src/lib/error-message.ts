/**
 * Returns a safe, operator-facing message for router and query boundaries.
 *
 * Router boundaries intentionally accept unknown thrown values. Avoiding a
 * string cast here keeps strict TypeScript useful without leaking arbitrary
 * values into the UI.
 */
export function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim()
    ? error.message
    : fallback;
}

/** Returns an optional correlation reference supplied by a server error. */
export function errorDigest(error: unknown): string {
  if (typeof error !== "object" || error === null || !("digest" in error)) {
    return "";
  }

  const digest = (error as { digest?: unknown }).digest;
  return digest == null ? "" : String(digest);
}
