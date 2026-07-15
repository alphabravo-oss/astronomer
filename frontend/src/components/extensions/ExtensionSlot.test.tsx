import type { MockedFunction } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { ExtensionSlot } from './ExtensionSlot';
import type { ExtensionMount, ExtensionPointKind } from '@/lib/api/extensions';

// Drive the slot off mocked runtime hooks so these tests cover the slot's own
// logic (tier derivation, empty-collapse, error isolation) without React Query.
vi.mock('./ExtensionProvider', () => ({
  __esModule: true,
  useExtensionRuntime: vi.fn(),
  useExtensionMounts: vi.fn(),
}));

// The default Tier-1 path renders a DeclarativeWidget (which fetches via React
// Query). Mock it to a marker so the slot's own logic stays under test here; the
// renderers have their own tests.
vi.mock('./DeclarativeWidget', () => ({
  __esModule: true,
  DeclarativeWidget: ({ extensionName }: { extensionName: string }) => (
    <div data-testid="declarative-widget">{extensionName}</div>
  ),
}));

// Tier-2 selects SandboxedExtension (the sandboxed iframe + bridge). Mock it to a
// marker so the slot's tier-derivation logic stays under test here; the bridge
// component has its own tests (SandboxedExtension.test.tsx).
vi.mock('./SandboxedExtension', () => ({
  __esModule: true,
  SandboxedExtension: ({ mount }: { mount: ExtensionMount }) => (
    <div data-testid="sandboxed-extension">{mount.extension}</div>
  ),
}));

import { useExtensionRuntime, useExtensionMounts } from './ExtensionProvider';

function withClient(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{ui}</QueryClientProvider>;
}

const mockedRuntime = useExtensionRuntime as MockedFunction<typeof useExtensionRuntime>;
const mockedMounts = useExtensionMounts as MockedFunction<typeof useExtensionMounts>;

function mount(over: Partial<ExtensionMount> = {}): ExtensionMount {
  return {
    extension: 'cost',
    displayName: 'Cost Insights',
    point: 'dashboardWidget',
    pointId: 'cost-summary',
    tier: 1,
    render: { declarative: { kind: 'table', dataSource: 'd1' } },
    ...over,
  };
}

function setup(mounts: ExtensionMount[], isLoading = false) {
  mockedRuntime.mockReturnValue({
    registry: {} as never,
    isLoading,
    isError: false,
  });
  mockedMounts.mockReturnValue(mounts);
}

const POINT: ExtensionPointKind = 'dashboardWidget';

describe('ExtensionSlot', () => {
  afterEach(() => vi.clearAllMocks());

  it('renders nothing when no extension is mounted at the point', () => {
    setup([]);
    const { container } = render(<ExtensionSlot point={POINT} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing while the registry is still loading', () => {
    setup([mount()], /* isLoading */ true);
    const { container } = render(<ExtensionSlot point={POINT} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders a Tier-1 declarative mount with the DeclarativeWidget renderer', () => {
    setup([mount()]);
    render(withClient(<ExtensionSlot point={POINT} />));
    expect(screen.getByTestId('declarative-widget')).toHaveTextContent('cost');
  });

  it('renders a Tier-2 bundle with the SandboxedExtension (iframe) renderer', () => {
    setup([
      mount({
        tier: 2,
        render: {
          bundle: {
            url: 'https://cdn.example/b.js',
            sha256: 'sha256:' + 'a'.repeat(64),
            integrity: 'sha384-x',
            entry: 'index.js',
            sandboxOrigin: 'https://ext.sandbox.local',
            component: 'Cost',
          },
        },
      }),
    ]);
    render(withClient(<ExtensionSlot point={POINT} />));
    expect(screen.getByTestId('sandboxed-extension')).toHaveTextContent('cost');
    expect(screen.queryByTestId('declarative-widget')).not.toBeInTheDocument();
  });

  it('passes each mount + context to a custom renderer', () => {
    const m = mount();
    setup([m]);
    const renderer = vi.fn(() => <div>rendered</div>);
    render(<ExtensionSlot point={POINT} context={{ clusterId: 'c1' }} render={renderer} />);
    expect(screen.getByText('rendered')).toBeInTheDocument();
    expect(renderer).toHaveBeenCalledWith(m, { clusterId: 'c1' });
  });

  it('isolates a throwing extension behind its error boundary', () => {
    // One throws, one renders — the good one must still mount.
    const good = mount({ extension: 'ok', pointId: 'ok-1', displayName: 'OK Ext' });
    const bad = mount({ extension: 'boom', pointId: 'boom-1', displayName: 'Boom Ext' });
    setup([good, bad]);
    const renderer = (mnt: ExtensionMount) => {
      if (mnt.extension === 'boom') throw new Error('hostile');
      return <div>good-content</div>;
    };
    // Silence the expected React error-boundary console noise.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    render(<ExtensionSlot point={POINT} render={renderer} />);
    spy.mockRestore();

    expect(screen.getByText('good-content')).toBeInTheDocument();
    expect(screen.getByText('boom')).toBeInTheDocument(); // boundary names the failed extension
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('applies the container className when mounts exist', () => {
    setup([mount()]);
    const { container } = render(withClient(<ExtensionSlot point={POINT} className="grid gap-3" />));
    const slot = container.querySelector('[data-extension-slot="dashboardWidget"]');
    expect(slot).toHaveClass('grid', 'gap-3');
  });
});
