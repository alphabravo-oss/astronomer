export type OperationPhase =
  | "idle"
  | "submitting"
  | "queued"
  | "running"
  | "partial"
  | "failed"
  | "succeeded";

export interface OperationSnapshot {
  id: string;
  status: string;
  errorMessage?: string;
}

export class OperationFailedError extends Error {
  readonly operation: OperationSnapshot;

  constructor(operation: OperationSnapshot) {
    super(
      operation.errorMessage || `Operation ${operation.id} ${operation.status}`,
    );
    this.name = "OperationFailedError";
    this.operation = operation;
  }
}

export class OperationPartialError extends Error {
  readonly operation: OperationSnapshot;

  constructor(operation: OperationSnapshot) {
    super(
      operation.errorMessage || `Operation ${operation.id} completed partially`,
    );
    this.name = "OperationPartialError";
    this.operation = operation;
  }
}

export function createIdempotencyKey(prefix: string): string {
  const suffix =
    globalThis.crypto?.randomUUID?.() ??
    `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `${prefix}:${suffix}`.slice(0, 128);
}

export function operationPhase(status: string): OperationPhase {
  switch (status.trim().toLowerCase()) {
    case "pending":
    case "queued":
    case "accepted":
      return "queued";
    case "running":
    case "processing":
    case "delivering":
    case "retrying":
      return "running";
    case "partial":
    case "blocked":
      return "partial";
    case "completed":
    case "succeeded":
    case "success":
    case "delivered":
      return "succeeded";
    case "failed":
    case "failure":
    case "dead":
    case "error":
    case "cancelled":
      return "failed";
    default:
      return "running";
  }
}

export function isTerminalOperation(status: string): boolean {
  const phase = operationPhase(status);
  return phase === "succeeded" || phase === "partial" || phase === "failed";
}

function abortError(): DOMException {
  return new DOMException("Operation polling cancelled", "AbortError");
}

async function delay(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) throw abortError();
  await new Promise<void>((resolve, reject) => {
    const onAbort = () => {
      globalThis.clearTimeout(timer);
      reject(abortError());
    };
    const timer = globalThis.setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

export async function pollOperation<T extends OperationSnapshot>(
  initial: T,
  read: (id: string, signal?: AbortSignal, operation?: T) => Promise<T>,
  options: {
    signal?: AbortSignal;
    onUpdate?: (operation: T) => void;
    maxAttempts?: number;
    maxDurationMs?: number;
    initialDelayMs?: number;
    maxDelayMs?: number;
  } = {},
): Promise<T> {
  const maxAttempts = options.maxAttempts ?? 300;
  const maxDurationMs = options.maxDurationMs ?? 20 * 60_000;
  const initialDelayMs = options.initialDelayMs ?? 500;
  const maxDelayMs = options.maxDelayMs ?? 5_000;
  let operation = initial;
  const deadline = Date.now() + maxDurationMs;
  options.onUpdate?.(operation);

  for (let attempt = 0; !isTerminalOperation(operation.status); attempt += 1) {
    if (attempt >= maxAttempts || Date.now() >= deadline) {
      throw new Error(
        `Operation ${operation.id} is still ${operation.status}; polling timed out`,
      );
    }
    await delay(
      Math.min(maxDelayMs, initialDelayMs * 2 ** Math.min(attempt, 4)),
      options.signal,
    );
    operation = await read(operation.id, options.signal, operation);
    options.onUpdate?.(operation);
  }
  if (operationPhase(operation.status) === "failed") {
    throw new OperationFailedError(operation);
  }
  if (operationPhase(operation.status) === "partial") {
    throw new OperationPartialError(operation);
  }
  return operation;
}
