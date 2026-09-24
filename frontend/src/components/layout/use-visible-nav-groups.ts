import { useCallback, useState } from "react";

/** Flyouts are transient; opening one must not change persisted accordions. */
export function useVisibleNavGroups(
  collapsed: boolean,
  expandedGroups: Set<string>,
) {
  const [flyouts, setFlyouts] = useState<Set<string>>(() => new Set());
  const onFlyoutChange = useCallback((label: string, open: boolean) => {
    setFlyouts((current) => {
      if (current.has(label) === open) return current;
      const next = new Set(current);
      if (open) next.add(label);
      else next.delete(label);
      return next;
    });
  }, []);
  return {
    visibleGroups: collapsed ? flyouts : expandedGroups,
    onFlyoutChange,
  };
}
