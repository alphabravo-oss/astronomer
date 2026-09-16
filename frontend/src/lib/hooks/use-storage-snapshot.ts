import { useCallback, useSyncExternalStore } from "react";

const STORAGE_CHANGED = "astronomer:storage-changed";
const emptySnapshot = () => null;

/** Browser storage is an external source; snapshots are stable primitive values. */
export function useStorageSnapshot(key?: string) {
  const subscribe = useCallback(
    (notify: () => void) => {
      const onStorage = (event: StorageEvent) => {
        if (!event.key || event.key === key) notify();
      };
      window.addEventListener("storage", onStorage);
      window.addEventListener(STORAGE_CHANGED, notify);
      return () => {
        window.removeEventListener("storage", onStorage);
        window.removeEventListener(STORAGE_CHANGED, notify);
      };
    },
    [key],
  );
  const read = useCallback(() => {
    try {
      return key ? window.localStorage.getItem(key) : null;
    } catch {
      return null;
    }
  }, [key]);
  const raw = useSyncExternalStore(subscribe, read, emptySnapshot);
  const write = useCallback(
    (value: unknown) => {
      if (!key) return;
      try {
        window.localStorage.setItem(key, JSON.stringify(value));
        window.dispatchEvent(new Event(STORAGE_CHANGED));
      } catch {
        /* The caller retains its in-memory draft when storage is unavailable. */
      }
    },
    [key],
  );
  return [raw, write] as const;
}
