import { tabIdFor, useWindowManagerStore } from '@/lib/window-manager-store';

const MIN_HEIGHT = 200;
const DEFAULT_HEIGHT = 420;

function makeLogsTab(pod: string, container?: string) {
  return {
    kind: 'logs' as const,
    clusterId: 'c1',
    namespace: 'default',
    pod,
    container,
  };
}

function resetStore() {
  useWindowManagerStore.setState({
    tabs: [],
    activeTabId: null,
    open: false,
    minimized: false,
    height: DEFAULT_HEIGHT,
  });
}

describe('tabIdFor', () => {
  it('builds a deterministic id from kind/cluster/namespace/pod/container', () => {
    expect(tabIdFor(makeLogsTab('web', 'app'))).toBe('logs:c1:default:web:app');
  });

  it('uses "_" as the container placeholder when container is unset', () => {
    expect(tabIdFor(makeLogsTab('web'))).toBe('logs:c1:default:web:_');
    expect(tabIdFor(makeLogsTab('web', ''))).toBe('logs:c1:default:web:_');
  });

  it('distinguishes kinds for the same pod/container', () => {
    const exec = { ...makeLogsTab('web', 'app'), kind: 'exec' as const };
    expect(tabIdFor(exec)).toBe('exec:c1:default:web:app');
    expect(tabIdFor(exec)).not.toBe(tabIdFor(makeLogsTab('web', 'app')));
  });
});

describe('useWindowManagerStore', () => {
  beforeEach(() => {
    resetStore();
  });

  describe('addTab dedupe', () => {
    it('reuses the existing tab when the same identity is added twice', () => {
      const { addTab } = useWindowManagerStore.getState();
      const first = addTab(makeLogsTab('web', 'app'));
      const second = addTab(makeLogsTab('web', 'app'));

      expect(second).toBe(first);
      expect(useWindowManagerStore.getState().tabs).toHaveLength(1);
    });

    it('activates, opens, and unminimizes the drawer on dedupe hit', () => {
      const { addTab } = useWindowManagerStore.getState();
      const id = addTab(makeLogsTab('web'));
      addTab(makeLogsTab('other'));
      useWindowManagerStore.setState({ minimized: true, open: false });

      addTab(makeLogsTab('web'));

      const state = useWindowManagerStore.getState();
      expect(state.activeTabId).toBe(id);
      expect(state.open).toBe(true);
      expect(state.minimized).toBe(false);
    });

    it('honors an explicit id override', () => {
      const { addTab } = useWindowManagerStore.getState();
      const id = addTab({ ...makeLogsTab('web'), id: 'custom' });
      expect(id).toBe('custom');
      expect(useWindowManagerStore.getState().tabs[0].id).toBe('custom');
    });

    it('creates separate tabs for different containers on the same pod', () => {
      const { addTab } = useWindowManagerStore.getState();
      addTab(makeLogsTab('web', 'app'));
      addTab(makeLogsTab('web', 'sidecar'));
      expect(useWindowManagerStore.getState().tabs).toHaveLength(2);
    });
  });

  describe('LRU eviction at maxTabs', () => {
    it('evicts the oldest tab when an 11th tab is added', () => {
      const { addTab } = useWindowManagerStore.getState();
      for (let i = 1; i <= 10; i++) {
        addTab(makeLogsTab(`pod-${i}`));
      }
      expect(useWindowManagerStore.getState().tabs).toHaveLength(10);

      const newId = addTab(makeLogsTab('pod-11'));

      const state = useWindowManagerStore.getState();
      expect(state.tabs).toHaveLength(10);
      expect(state.tabs.map((t) => t.pod)).toEqual([
        'pod-2',
        'pod-3',
        'pod-4',
        'pod-5',
        'pod-6',
        'pod-7',
        'pod-8',
        'pod-9',
        'pod-10',
        'pod-11',
      ]);
      expect(state.activeTabId).toBe(newId);
    });

    it('does not evict when re-adding an existing tab at the cap', () => {
      const { addTab } = useWindowManagerStore.getState();
      for (let i = 1; i <= 10; i++) {
        addTab(makeLogsTab(`pod-${i}`));
      }

      addTab(makeLogsTab('pod-1'));

      const state = useWindowManagerStore.getState();
      expect(state.tabs).toHaveLength(10);
      expect(state.tabs[0].pod).toBe('pod-1');
    });
  });

  describe('closeTab next-tab selection', () => {
    it('prefers the tab to the right of the closed active tab', () => {
      const { addTab } = useWindowManagerStore.getState();
      const a = addTab(makeLogsTab('a'));
      const b = addTab(makeLogsTab('b'));
      const c = addTab(makeLogsTab('c'));
      useWindowManagerStore.getState().setActive(b);

      useWindowManagerStore.getState().closeTab(b);

      const state = useWindowManagerStore.getState();
      expect(state.tabs.map((t) => t.id)).toEqual([a, c]);
      expect(state.activeTabId).toBe(c);
    });

    it('falls back to the tab on the left when there is no right neighbor', () => {
      const { addTab } = useWindowManagerStore.getState();
      const a = addTab(makeLogsTab('a'));
      const b = addTab(makeLogsTab('b'));

      useWindowManagerStore.getState().closeTab(b);

      const state = useWindowManagerStore.getState();
      expect(state.activeTabId).toBe(a);
    });

    it('keeps the active tab when closing a non-active tab', () => {
      const { addTab } = useWindowManagerStore.getState();
      const a = addTab(makeLogsTab('a'));
      const b = addTab(makeLogsTab('b'));
      useWindowManagerStore.getState().setActive(b);

      useWindowManagerStore.getState().closeTab(a);

      expect(useWindowManagerStore.getState().activeTabId).toBe(b);
    });

    it('clears the active tab and closes the drawer when the last tab closes', () => {
      const { addTab } = useWindowManagerStore.getState();
      const only = addTab(makeLogsTab('a'));

      useWindowManagerStore.getState().closeTab(only);

      const state = useWindowManagerStore.getState();
      expect(state.tabs).toHaveLength(0);
      expect(state.activeTabId).toBeNull();
      expect(state.open).toBe(false);
    });

    it('is a no-op for an unknown id', () => {
      const { addTab } = useWindowManagerStore.getState();
      const a = addTab(makeLogsTab('a'));

      useWindowManagerStore.getState().closeTab('nope');

      const state = useWindowManagerStore.getState();
      expect(state.tabs.map((t) => t.id)).toEqual([a]);
      expect(state.activeTabId).toBe(a);
    });
  });

  describe('setHeight clamping', () => {
    const setInnerHeight = (px: number) => {
      Object.defineProperty(window, 'innerHeight', {
        configurable: true,
        writable: true,
        value: px,
      });
    };

    it('clamps below the 200px minimum up to the minimum', () => {
      setInnerHeight(768);
      useWindowManagerStore.getState().setHeight(50);
      expect(useWindowManagerStore.getState().height).toBe(MIN_HEIGHT);
    });

    it('clamps above the viewport ceiling to innerHeight - 80', () => {
      setInnerHeight(768);
      useWindowManagerStore.getState().setHeight(5000);
      expect(useWindowManagerStore.getState().height).toBe(768 - 80);
    });

    it('passes in-range values through unchanged', () => {
      setInnerHeight(768);
      useWindowManagerStore.getState().setHeight(400);
      expect(useWindowManagerStore.getState().height).toBe(400);
    });

    it('never clamps the ceiling below the minimum on tiny viewports', () => {
      setInnerHeight(120);
      useWindowManagerStore.getState().setHeight(5000);
      expect(useWindowManagerStore.getState().height).toBe(MIN_HEIGHT);
    });
  });
});
