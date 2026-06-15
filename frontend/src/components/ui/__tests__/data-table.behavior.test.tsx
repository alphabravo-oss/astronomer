import { fireEvent, render, screen } from '@testing-library/react';
import { DataTable, type Column } from '@/components/ui/data-table';

type Row = { id: string; name: string; size: number };

const rows: Row[] = [
  { id: 'b', name: 'Banana', size: 30 },
  { id: 'a', name: 'Apple', size: 10 },
  { id: 'c', name: 'Cherry', size: 20 },
];

const columns: Column<Row>[] = [
  { key: 'name', header: 'Name', accessor: (r) => r.name },
  { key: 'size', header: 'Size', accessor: (r) => String(r.size), sortAccessor: (r) => r.size },
];

// Body rows are every table row except the header row.
const bodyRowText = () => screen.getAllByRole('row').slice(1).map((r) => r.textContent ?? '');

describe('DataTable behavior (TanStack Table engine)', () => {
  it('keeps source order until a column is sorted', () => {
    render(<DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} />);
    expect(bodyRowText().map((t) => t.replace(/[0-9]/g, ''))).toEqual(['Banana', 'Apple', 'Cherry']);
  });

  it('sorts numerically via sortAccessor, ascending then descending', () => {
    render(<DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} />);
    const sizeHeader = screen.getByRole('columnheader', { name: /size/i });

    fireEvent.click(sizeHeader); // asc: 10, 20, 30
    expect(bodyRowText()[0]).toContain('Apple');

    fireEvent.click(sizeHeader); // desc: 30, 20, 10
    expect(bodyRowText()[0]).toContain('Banana');
  });

  it('filters rows with the global search box across visible columns', () => {
    render(<DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} />);
    fireEvent.change(screen.getByPlaceholderText('Search...'), { target: { value: 'Apple' } });
    const body = bodyRowText();
    expect(body).toHaveLength(1);
    expect(body[0]).toContain('Apple');
  });

  it('paginates and navigates between pages', () => {
    render(<DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} pageSize={1} />);
    expect(screen.getByText('Showing 1-1 of 3')).toBeInTheDocument();
    expect(bodyRowText()).toHaveLength(1);
    expect(bodyRowText()[0]).toContain('Banana');

    fireEvent.click(screen.getByRole('button', { name: '2' }));
    expect(bodyRowText()[0]).toContain('Apple');
  });

  it('persists column visibility to localStorage when persistKey is set', () => {
    window.localStorage.clear();
    const { unmount } = render(
      <DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} persistKey="test-table" />
    );
    fireEvent.click(screen.getByRole('button', { name: /columns/i }));
    fireEvent.click(screen.getAllByRole('checkbox')[1]); // hide Size

    expect(JSON.parse(window.localStorage.getItem('dt:test-table:visibility')!)).toMatchObject({
      size: false,
    });

    // Remount: the hidden column is restored from storage.
    unmount();
    render(
      <DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} persistKey="test-table" />
    );
    expect(screen.queryByRole('columnheader', { name: /size/i })).not.toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: /name/i })).toBeInTheDocument();
    window.localStorage.clear();
  });

  it('filters rows via a faceted multi-select column filter', () => {
    type R = { id: string; name: string; status: string };
    const facetRows: R[] = [
      { id: '1', name: 'Alpha', status: 'ready' },
      { id: '2', name: 'Bravo', status: 'pending' },
      { id: '3', name: 'Charlie', status: 'ready' },
    ];
    const facetColumns: Column<R>[] = [
      { key: 'name', header: 'Name', accessor: (r) => r.name },
      {
        key: 'status',
        header: 'Status',
        accessor: (r) => r.status,
        sortAccessor: (r) => r.status,
        filter: { label: 'Status' },
      },
    ];
    render(
      <DataTable data={facetRows} columns={facetColumns} keyExtractor={(r) => r.id} searchable={false} />
    );

    // Open the Status facet and select 'ready'.
    fireEvent.click(screen.getByRole('button', { name: /status/i }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'ready' }));

    const body = bodyRowText();
    expect(body).toHaveLength(2);
    expect(body.join(' ')).toContain('Alpha');
    expect(body.join(' ')).toContain('Charlie');
    expect(body.join(' ')).not.toContain('Bravo');
  });

  it('server-side mode uses rowCount for paging and reports page changes without client slicing', () => {
    const onPaginationChange = jest.fn();
    const pageRows: Row[] = [
      { id: '1', name: 'A', size: 1 },
      { id: '2', name: 'B', size: 2 },
    ];
    render(
      <DataTable
        data={pageRows}
        columns={columns}
        keyExtractor={(r) => r.id}
        pageSize={2}
        searchable={false}
        serverSide={{
          rowCount: 10,
          pagination: { pageIndex: 0, pageSize: 2 },
          onPaginationChange,
        }}
      />
    );

    // Footer reflects the SERVER total (10), not the 2 loaded rows.
    expect(screen.getByText('Showing 1-2 of 10')).toBeInTheDocument();
    // The 2 loaded rows are shown as-is (no further client-side slicing).
    expect(bodyRowText()).toHaveLength(2);

    // Navigating hands the new page index back to the caller.
    fireEvent.click(screen.getByRole('button', { name: '2' }));
    expect(onPaginationChange).toHaveBeenCalledWith(expect.objectContaining({ pageIndex: 1 }));
  });

  it('toggles column visibility but refuses to hide the last column', () => {
    render(<DataTable data={rows} columns={columns} keyExtractor={(r) => r.id} />);
    fireEvent.click(screen.getByRole('button', { name: /columns/i }));

    const checkboxes = screen.getAllByRole('checkbox'); // [Name, Size] in the dropdown
    fireEvent.click(checkboxes[1]); // hide Size
    expect(screen.queryByRole('columnheader', { name: /size/i })).not.toBeInTheDocument();

    fireEvent.click(checkboxes[0]); // attempt to hide Name (the last visible) → refused
    expect(screen.getByRole('columnheader', { name: /name/i })).toBeInTheDocument();
  });
});
