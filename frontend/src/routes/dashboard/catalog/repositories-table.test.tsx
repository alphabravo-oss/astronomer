import { render, screen, within } from '@testing-library/react';
import { camelizeKeys } from '@/lib/camelize';
import type { HelmRepository } from '@/types';
import { RepositoriesTable } from './-repositories-table';

/**
 * The catalog Repositories table, driven by a REAL snake_case wire payload.
 *
 * The fixture below is what `GET /api/v1/catalog/repositories/` actually puts
 * on the wire, and it is passed through the same `camelizeKeys` the axios
 * response interceptor applies (src/lib/api.ts) rather than being hand-written
 * in camelCase. That is the point: the bug this guards against lived exactly
 * in the gap between "what the server sends" and "what the component believes
 * it receives", and a camelCase fixture would have papered over it.
 *
 * Pre-fix, the Charts column rendered an EMPTY cell for every repository.
 * `row.chartCount` was `undefined` because no chart count existed anywhere in
 * the API — no column, no query, no OpenAPI field — and React renders
 * `undefined` as nothing at all. The assertions here are on rendered text for
 * that reason: asserting a field merely "exists" is what let this ship.
 */

const wireRepositories = [
  {
    id: 'repo-1',
    name: 'bitnami',
    url: 'https://charts.bitnami.com/bitnami',
    repo_type: 'helm',
    description: '',
    is_default: true,
    auth_type: 'none',
    auth_config: {},
    enabled: true,
    last_synced_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    last_sync_attempted_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    last_sync_error: '',
    created_by_id: null,
    created_at: '2026-07-01T00:00:00Z',
    updated_at: '2026-07-28T10:30:00Z',
    owner_project_id: null,
    chart_count: 42,
  },
  {
    id: 'repo-2',
    name: 'never-synced',
    url: 'https://example.invalid',
    repo_type: 'oci',
    description: '',
    is_default: false,
    auth_type: 'none',
    auth_config: {},
    enabled: true,
    last_synced_at: null,
    last_sync_attempted_at: null,
    last_sync_error: '',
    created_by_id: null,
    created_at: '2026-07-01T00:00:00Z',
    updated_at: '2026-07-01T00:00:00Z',
    owner_project_id: null,
    chart_count: 0,
  },
];

// Exactly what the axios interceptor hands the component.
const repos = camelizeKeys(wireRepositories) as unknown as HelmRepository[];

function renderTable() {
  return render(
    <RepositoriesTable repos={repos} onSync={vi.fn()} onDelete={vi.fn()} />,
  );
}

function rowFor(name: string): HTMLElement {
  const row = screen.getByText(name).closest('tr');
  if (!row) throw new Error(`no table row for repository ${name}`);
  return row;
}

describe('catalog Repositories table', () => {
  it('renders the chart count from the API payload, not a blank cell', () => {
    renderTable();
    expect(within(rowFor('bitnami')).getByText('42')).toBeInTheDocument();
  });

  it('renders a relative last-synced time instead of "Never" for a synced repo', () => {
    renderTable();
    const row = rowFor('bitnami');
    expect(within(row).getByText(/about 2 hours ago/)).toBeInTheDocument();
    expect(within(row).queryByText('Never')).not.toBeInTheDocument();
  });

  it('reports Never and 0 for a repository that has never synced', () => {
    renderTable();
    const row = rowFor('never-synced');
    expect(within(row).getByText('Never')).toBeInTheDocument();
    expect(within(row).getByText('0')).toBeInTheDocument();
  });

  it('renders 0 rather than an empty cell when chart_count is absent entirely', () => {
    // The pre-fix wire shape: no chart count field at all. This is the exact
    // payload that made the column render blank.
    const legacy = camelizeKeys([
      { ...wireRepositories[0], chart_count: undefined },
    ]) as unknown as HelmRepository[];
    render(<RepositoriesTable repos={legacy} onSync={vi.fn()} onDelete={vi.fn()} />);
    expect(within(rowFor('bitnami')).getByText('0')).toBeInTheDocument();
  });
});
