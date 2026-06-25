import { render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { DeclarativeWidget } from './DeclarativeWidget';
import * as extensionsApi from '@/lib/api/extensions';
import type { DeclarativeWidget as Spec, ExtensionDataResponse } from '@/lib/api/extensions';

jest.mock('@/lib/api/extensions', () => ({
  __esModule: true,
  fetchExtensionData: jest.fn(),
}));

const mockedFetch = extensionsApi.fetchExtensionData as jest.MockedFunction<
  typeof extensionsApi.fetchExtensionData
>;

function wrap(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

function ok(data: unknown, shape: ExtensionDataResponse['shape'] = 'list'): ExtensionDataResponse {
  return { data, shape, meta: { dataSourceId: 'd' } };
}

const tableSpec: Spec = {
  kind: 'table',
  dataSource: 'podCost',
  fields: [
    { path: 'namespace', label: 'Namespace', format: 'text' },
    { path: 'usd', label: 'Cost', format: 'currency' },
  ],
  emptyText: 'No cost data',
};

describe('DeclarativeWidget — table', () => {
  afterEach(() => jest.clearAllMocks());

  it('fetches via the data proxy and renders projected, formatted rows', async () => {
    mockedFetch.mockResolvedValue(ok([{ namespace: 'team-a', usd: 12.5 }]));

    wrap(<DeclarativeWidget extensionName="cost" spec={tableSpec} context={{ clusterId: 'c1' }} />);

    expect(await screen.findByText('team-a')).toBeInTheDocument();
    expect(screen.getByText('$12.50')).toBeInTheDocument();
    expect(screen.getByText('Namespace')).toBeInTheDocument();
    expect(mockedFetch).toHaveBeenCalledWith('cost', 'podCost', { context: { clusterId: 'c1' } });
  });

  it('renders the manifest emptyText when the proxy returns no rows', async () => {
    mockedFetch.mockResolvedValue(ok([]));
    wrap(<DeclarativeWidget extensionName="cost" spec={tableSpec} />);
    expect(await screen.findByText('No cost data')).toBeInTheDocument();
  });

  it('renders an error state with retry when the proxy call fails', async () => {
    mockedFetch.mockRejectedValue(new Error('extension_rbac_denied'));
    wrap(<DeclarativeWidget extensionName="cost" spec={tableSpec} />);
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(screen.getByText('extension_rbac_denied')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument();
  });
});

describe('DeclarativeWidget — stat', () => {
  afterEach(() => jest.clearAllMocks());

  const statSpec: Spec = {
    kind: 'stat',
    dataSource: 'total',
    stat: { label: 'Total cost', value: { path: 'usd', label: 'usd', format: 'currency' } },
  };

  it('renders a single proxied object as a labelled stat', async () => {
    mockedFetch.mockResolvedValue(ok({ usd: 990 }, 'object'));
    wrap(<DeclarativeWidget extensionName="cost" spec={statSpec} />);
    expect(await screen.findByText('Total cost')).toBeInTheDocument();
    expect(screen.getByText('$990.00')).toBeInTheDocument();
  });

  it('shows empty text when the object response is empty', async () => {
    mockedFetch.mockResolvedValue(ok({}, 'object'));
    wrap(
      <DeclarativeWidget
        extensionName="cost"
        spec={{ ...statSpec, emptyText: 'nothing yet' }}
      />,
    );
    expect(await screen.findByText('nothing yet')).toBeInTheDocument();
  });
});

describe('DeclarativeWidget — chart', () => {
  afterEach(() => jest.clearAllMocks());

  it('renders an SVG chart from series rows', async () => {
    mockedFetch.mockResolvedValue(
      ok([
        { day: 'Mon', hits: 3 },
        { day: 'Tue', hits: 7 },
      ]),
    );
    const spec: Spec = { kind: 'chart', dataSource: 's', chart: { type: 'bar', x: 'day', y: ['hits'] } };
    wrap(<DeclarativeWidget extensionName="cost" spec={spec} />);
    expect(await screen.findByRole('img', { name: /bar chart/i })).toBeInTheDocument();
    expect(screen.getByText('Mon')).toBeInTheDocument();
  });
});

describe('DeclarativeWidget — form', () => {
  afterEach(() => jest.clearAllMocks());

  it('renders a form without fetching read data', () => {
    const spec: Spec = {
      kind: 'form',
      dataSource: 'unused',
      form: {
        submit: 'createThing',
        submitLabel: 'Create',
        inputs: [{ name: 'title', label: 'Title', type: 'text', required: true }],
      },
    };
    wrap(<DeclarativeWidget extensionName="cost" spec={spec} />);
    expect(screen.getByLabelText(/Title/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create' })).toBeInTheDocument();
    // Forms write on submit only — no read fetch on mount.
    expect(mockedFetch).not.toHaveBeenCalled();
  });
});
