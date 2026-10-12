import axios, {
  AxiosError,
  CanceledError,
  type GenericAbortSignal,
} from "axios";

/** Refresh ownership belongs to the session, never to an individual read. */
let sharedRefresh: Promise<void> | undefined;
let cancelShared: (() => void) | undefined;

/** Logout ends the old session lifetime, including refresh waiters. */
export function invalidateSessionRefresh() {
  cancelShared?.();
  sharedRefresh = undefined;
  cancelShared = undefined;
}
export const REFRESH_DEADLINE_MS = 30_000;

export function refreshSession(
  url: string,
  headers: Record<string, string>,
  onFailure: () => void,
): Promise<void> {
  if (sharedRefresh) return sharedRefresh;
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout>;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      // Settle even if an adapter ignores abort or its timeout option.
      reject(new AxiosError("Session refresh timed out", "ETIMEDOUT"));
      controller.abort();
    }, REFRESH_DEADLINE_MS);
  });
  const revoked = new Promise<never>((_, reject) => {
    cancelShared = () => {
      reject(new CanceledError("Session ended"));
      controller.abort();
    };
  });
  const ownedRefresh = Promise.race([
    axios.post(
      url,
      {},
      {
        headers,
        withCredentials: true,
        signal: controller.signal,
        timeout: REFRESH_DEADLINE_MS,
      },
    ),
    deadline,
    revoked,
  ])
    .then(() => undefined)
    .catch((error: unknown) => {
      if (!axios.isCancel(error)) onFailure();
      throw error;
    })
    .finally(() => {
      clearTimeout(timer);
      if (sharedRefresh === ownedRefresh) {
        sharedRefresh = undefined;
        cancelShared = undefined;
      }
    });
  // The session owner observes failure even when every waiter aborts before attachment.
  void ownedRefresh.catch(() => undefined);
  sharedRefresh = ownedRefresh;
  return ownedRefresh;
}

/** Aborting observation removes its listener without cancelling other waiters. */
export function waitForRefresh(
  refresh: Promise<void>,
  signal?: GenericAbortSignal,
): Promise<void> {
  if (!signal) return refresh;
  return new Promise((resolve, reject) => {
    const cleanup = () => signal.removeEventListener?.("abort", cancel);
    const cancel = () => {
      cleanup();
      reject(new CanceledError("Request canceled while refreshing session"));
    };
    if (signal.aborted) {
      cancel();
      return;
    }
    signal.addEventListener?.("abort", cancel, { once: true });
    refresh.then(
      () => {
        cleanup();
        resolve();
      },
      (error: unknown) => {
        cleanup();
        reject(error);
      },
    );
  });
}
