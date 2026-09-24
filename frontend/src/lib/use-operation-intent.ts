import { useRef } from "react";
import { createIdempotencyKey } from "@/lib/api/idempotency";

/** Retain the request identity after an uncertain response; changed input is a new intent. */
export function useOperationIntent() {
  const current = useRef<{ fingerprint: string; key: string } | null>(null);
  return {
    keyFor(payload: unknown) {
      const fingerprint = JSON.stringify(payload);
      if (!current.current || current.current.fingerprint !== fingerprint)
        current.current = { fingerprint, key: createIdempotencyKey() };
      return current.current.key;
    },
    complete() {
      current.current = null;
    },
  };
}
