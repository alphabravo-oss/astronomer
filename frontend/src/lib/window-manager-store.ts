import { persistedStore } from '@/lib/persisted-store';
import { createStoreHook } from '@/lib/store-hook';

// ============================================================
// Window Manager Store (Rancher-style bottom drawer)
// ============================================================
//
// The dashboard pins a sliding bottom drawer that hosts multiple
// concurrent logs/exec tabs. Tabs are pure ephemeral state (live WS
// connections); we only persist drawer chrome (height + minimized).

export type WindowTabKind = 'logs' | 'exec';

export type WindowTab =
  | {
      id: string;
      kind: 'logs';
      clusterId: string;
      namespace: string;
      pod: string;
      container?: string;
    }
  | {
      id: string;
      kind: 'exec';
      clusterId: string;
      namespace: string;
      pod: string;
      container?: string;
      shell?: 'bash' | 'sh';
    };

type AddTabInput = Omit<WindowTab, 'id'> & { id?: string };

interface WindowManagerState extends Record<string, unknown> {
  tabs: WindowTab[];
  activeTabId: string | null;
  open: boolean;
  minimized: boolean;
  height: number; // px
  maxTabs: number;
  addTab: (tab: AddTabInput) => string;
  closeTab: (id: string) => void;
  closeAll: () => void;
  setActive: (id: string) => void;
  toggleMinimize: () => void;
  setMinimized: (m: boolean) => void;
  setOpen: (open: boolean) => void;
  setHeight: (px: number) => void;
}

const MAX_TABS = 10;
const DEFAULT_HEIGHT = 420;
const MIN_HEIGHT = 200;

// Stable deterministic id for a tab so opening "logs" twice on the
// same (pod, container) reuses the existing tab rather than spawning
// a duplicate WS connection.
export function tabIdFor(t: AddTabInput): string {
  const container = t.container || '_';
  return `${t.kind}:${t.clusterId}:${t.namespace}:${t.pod}:${container}`;
}

function clampHeight(px: number): number {
  if (typeof window === 'undefined') {
    return Math.max(MIN_HEIGHT, px);
  }
  const max = Math.max(MIN_HEIGHT, window.innerHeight - 80);
  return Math.max(MIN_HEIGHT, Math.min(max, px));
}

export const useWindowManagerStore = createStoreHook(
  persistedStore<WindowManagerState>(
    {
      tabs: [],
      activeTabId: null,
      open: false,
      minimized: false,
      height: DEFAULT_HEIGHT,
      maxTabs: MAX_TABS,

      addTab: (input) => {
        const id = input.id || tabIdFor(input);
        const existing = useWindowManagerStore
          .getState()
          .tabs.find((t) => t.id === id);
        if (existing) {
          useWindowManagerStore.setState({ activeTabId: id, open: true, minimized: false });
          return id;
        }

        const tab = { ...(input as Omit<WindowTab, 'id'>), id } as WindowTab;
        useWindowManagerStore.setState((state) => {
          // Enforce hard cap by dropping the oldest tab. We avoid silently
          // failing because the user just clicked an action — show them
          // *something*, even if it means evicting the least-recently
          // used tab.
          let tabs = [...state.tabs, tab];
          if (tabs.length > state.maxTabs) {
            tabs = tabs.slice(tabs.length - state.maxTabs);
          }
          return {
            tabs,
            activeTabId: id,
            open: true,
            minimized: false,
          };
        });
        return id;
      },

      closeTab: (id) => {
        useWindowManagerStore.setState((state) => {
          const idx = state.tabs.findIndex((t) => t.id === id);
          if (idx < 0) return state;
          const tabs = state.tabs.filter((t) => t.id !== id);
          let activeTabId = state.activeTabId;
          if (state.activeTabId === id) {
            // Prefer the tab to the right; fall back to the left.
            const next = tabs[idx] ?? tabs[idx - 1] ?? null;
            activeTabId = next?.id ?? null;
          }
          return {
            tabs,
            activeTabId,
            open: tabs.length > 0 ? state.open : false,
          };
        });
      },

      closeAll: () => {
        useWindowManagerStore.setState({ tabs: [], activeTabId: null, open: false });
      },

      setActive: (id) => {
        useWindowManagerStore.setState((state) =>
          state.tabs.find((t) => t.id === id)
            ? { activeTabId: id, open: true, minimized: false }
            : state
        );
      },

      toggleMinimize: () =>
        useWindowManagerStore.setState((s) => ({ minimized: !s.minimized })),
      setMinimized: (m) => useWindowManagerStore.setState({ minimized: m }),
      setOpen: (open) => useWindowManagerStore.setState({ open }),
      setHeight: (px) => useWindowManagerStore.setState({ height: clampHeight(px) }),
    },
    {
      name: 'astronomer-window-manager',
      // Only chrome is persisted; live tabs are intentionally dropped on
      // reload because their WS connections can't survive a page load.
      partialize: (state) => ({
        height: state.height,
        minimized: state.minimized,
      }),
    },
  ),
);
