import type { MockedFunction } from 'vitest';
import type { ComponentProps } from 'react';
import { render, screen } from '@testing-library/react';
import { ExtensionNavItems, extensionSidebarHref } from './ExtensionNavItems';
import type { ExtensionMount } from '@/lib/api/extensions';

// Plain-anchor stand-in: these tests assert link text/href, not routing, and
// the real Link needs a <RouterProvider>.
vi.mock('@/lib/link', () => ({
  Link: ({ href, children, ...rest }: ComponentProps<'a'>) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

vi.mock('./ExtensionProvider', () => ({
  __esModule: true,
  useExtensionMounts: vi.fn(),
}));

import { useExtensionMounts } from './ExtensionProvider';

const mockedMounts = useExtensionMounts as MockedFunction<typeof useExtensionMounts>;

function sidebarMount(over: Partial<ExtensionMount> = {}): ExtensionMount {
  return {
    extension: 'cost',
    displayName: 'Cost Insights',
    point: 'sidebar',
    pointId: 'cost',
    tier: 1,
    render: { declarative: { kind: 'table', dataSource: 'd1' } },
    label: 'Cost',
    path: '/whatever/the/manifest/said',
    ...over,
  };
}

describe('extensionSidebarHref', () => {
  it('host-fixes the route under /dashboard/extensions/{name}', () => {
    expect(extensionSidebarHref('cost')).toBe('/dashboard/extensions/cost');
  });
  it('encodes the extension name into the path', () => {
    expect(extensionSidebarHref('a b')).toBe('/dashboard/extensions/a%20b');
  });
});

describe('ExtensionNavItems', () => {
  afterEach(() => vi.clearAllMocks());

  it('renders nothing (header included) when no sidebar mount exists', () => {
    mockedMounts.mockReturnValue([]);
    const { container } = render(<ExtensionNavItems pathname="/dashboard" collapsed={false} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders one nav link per sidebar mount, href derived from the name not the manifest path', () => {
    mockedMounts.mockReturnValue([sidebarMount()]);
    render(<ExtensionNavItems pathname="/dashboard" collapsed={false} />);
    const link = screen.getByRole('link', { name: /Cost/ });
    // Host-fixed href — never the path the (untrusted) manifest shipped.
    expect(link).toHaveAttribute('href', '/dashboard/extensions/cost');
    expect(link).not.toHaveAttribute('href', '/whatever/the/manifest/said');
    expect(screen.getByText('Extensions')).toBeInTheDocument();
  });

  it('falls back to extension name when no label is present', () => {
    mockedMounts.mockReturnValue([sidebarMount({ label: undefined, displayName: undefined })]);
    render(<ExtensionNavItems pathname="/dashboard" collapsed={false} />);
    expect(screen.getByRole('link', { name: /cost/ })).toBeInTheDocument();
  });

  it('omits the section header when collapsed', () => {
    mockedMounts.mockReturnValue([sidebarMount()]);
    render(<ExtensionNavItems pathname="/dashboard" collapsed={true} />);
    expect(screen.queryByText('Extensions')).not.toBeInTheDocument();
    expect(screen.getByRole('link')).toHaveAttribute('href', '/dashboard/extensions/cost');
  });
});
