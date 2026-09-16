import { useSyncExternalStore } from "react";

type StateUpdate<T> = Partial<T> | ((state: T) => Partial<T>);

export interface BrowserStateOptions<T> {
  storageKey?: string;
  version?: number;
  migrate?: (persistedState: unknown, version: number) => unknown;
  persist?: (state: T) => Record<string, unknown>;
}

export interface BrowserState<T extends Record<string, unknown>> {
  (): T;
  <Selected>(selector: (state: T) => Selected): Selected;
  getState(): T;
  setState(update: StateUpdate<T>): void;
}

/**
 * App-owned browser state for the small pieces of UI state that must outlive
 * a component tree. Server data belongs in TanStack Query; this utility is
 * deliberately limited to synchronous browser state and optional storage.
 */
export function createBrowserState<T extends Record<string, unknown>>(
  initial: T,
  options: BrowserStateOptions<T> = {},
): BrowserState<T> {
  const { storageKey, version = 0, migrate, persist } = options;
  let state = hydrate(initial, storageKey, version, migrate);
  const listeners = new Set<() => void>();

  const notify = () => listeners.forEach((listener) => listener());
  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  };
  const getSnapshot = () => state;

  function useBrowserState(): T;
  function useBrowserState<Selected>(
    selector: (current: T) => Selected,
  ): Selected;
  function useBrowserState<Selected>(
    selector?: (current: T) => Selected,
  ): T | Selected {
    const current = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
    return selector ? selector(current) : current;
  }

  useBrowserState.getState = () => state;
  useBrowserState.setState = (update: StateUpdate<T>) => {
    const partial = typeof update === "function" ? update(state) : update;
    state = { ...state, ...partial };
    if (storageKey) write(storageKey, version, persist?.(state) ?? state);
    notify();
  };

  return useBrowserState;
}

function hydrate<T extends Record<string, unknown>>(
  initial: T,
  storageKey: string | undefined,
  version: number,
  migrate: BrowserStateOptions<T>["migrate"],
): T {
  if (!storageKey || typeof window === "undefined") return initial;
  try {
    const raw = window.localStorage.getItem(storageKey);
    if (!raw) return initial;
    const envelope = JSON.parse(raw) as { state?: unknown; version?: number };
    const persisted =
      envelope.version === version
        ? envelope.state
        : migrate?.(envelope.state, envelope.version ?? 0);
    return persisted && typeof persisted === "object"
      ? { ...initial, ...(persisted as Partial<T>) }
      : initial;
  } catch {
    return initial;
  }
}

function write(
  storageKey: string,
  version: number,
  state: Record<string, unknown>,
): void {
  try {
    window.localStorage.setItem(storageKey, JSON.stringify({ state, version }));
  } catch {
    // Storage may be unavailable or full; in-memory state remains valid.
  }
}
