import {
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  type RefObject,
} from "react";
import {
  Virtualizer,
  elementScroll,
  observeElementOffset,
  observeElementRect,
  type VirtualItem,
} from "@tanstack/react-virtual";

interface VirtualRowsOptions {
  count: number;
  estimateSize: number;
  overscan: number;
  scrollRef: RefObject<HTMLDivElement | null>;
  enabled: boolean;
}

interface WindowSnapshot {
  items: readonly VirtualItem[];
  totalSize: number;
}

function createVirtualRowsController(initial: VirtualRowsOptions) {
  const listeners = new Set<() => void>();
  let snapshot: WindowSnapshot = { items: [], totalSize: 0 };
  let previousItems: VirtualItem[] | undefined;
  const publish = () => {
    const items = instance.getVirtualItems();
    const totalSize = instance.getTotalSize();
    if (items === previousItems && totalSize === snapshot.totalSize) return;
    previousItems = items;
    snapshot = { items: items.map((item) => ({ ...item })), totalSize };
    listeners.forEach((notify) => notify());
  };
  const resolveOptions = (options: VirtualRowsOptions) => ({
    count: options.count,
    overscan: options.overscan,
    enabled: options.enabled,
    estimateSize: () => options.estimateSize,
    getScrollElement: () => options.scrollRef.current,
    scrollToFn: elementScroll,
    observeElementRect,
    observeElementOffset,
    onChange: publish,
  });
  const instance = new Virtualizer<HTMLDivElement, Element>(
    resolveOptions(initial),
  );
  return {
    subscribe(notify: () => void) {
      listeners.add(notify);
      return () => {
        listeners.delete(notify);
      };
    },
    getSnapshot: () => snapshot,
    mount: () => instance._didMount(),
    update(options: VirtualRowsOptions) {
      instance.setOptions(resolveOptions(options));
      instance._willUpdate();
      publish();
    },
    measureElement: instance.measureElement,
    scrollToIndex: instance.scrollToIndex,
  };
}

/** A mutable DOM virtualizer is an external store, never a render-time facade. */
export function useVirtualRows(options: VirtualRowsOptions) {
  const [controller] = useState(() => createVirtualRowsController(options));
  const snapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  );
  useLayoutEffect(() => controller.mount(), [controller]);
  // Commit the current DOM container/options before publishing a new snapshot.
  useLayoutEffect(() => {
    controller.update(options);
  });
  return useMemo(
    () => ({
      ...snapshot,
      measureElement: controller.measureElement,
      scrollToIndex: controller.scrollToIndex,
    }),
    [controller, snapshot],
  );
}

export type VirtualRows = ReturnType<typeof useVirtualRows>;
