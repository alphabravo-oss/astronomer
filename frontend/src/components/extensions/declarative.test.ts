import {
  getByPath,
  formatValue,
  formatDuration,
  extractRows,
  extractObject,
  buildChartSeries,
  chartMax,
  isEmptyResponse,
  tableColumns,
} from './declarative';
import type { ChartSpec, ExtensionDataResponse } from '@/lib/api/extensions';

function res(data: unknown, shape: ExtensionDataResponse['shape'] = 'list'): ExtensionDataResponse {
  return { data, shape, meta: { dataSourceId: 'd' } };
}

describe('getByPath (JSONPath-lite)', () => {
  it('resolves nested object dot-paths', () => {
    expect(getByPath({ metadata: { name: 'pod-a' } }, 'metadata.name')).toBe('pod-a');
  });

  it('indexes into arrays by numeric segment', () => {
    expect(getByPath({ items: [{ id: 1 }, { id: 2 }] }, 'items.1.id')).toBe(2);
  });

  it('returns undefined for a missing key rather than throwing', () => {
    expect(getByPath({ a: {} }, 'a.b.c')).toBeUndefined();
  });

  it('returns undefined for out-of-range / non-numeric array access', () => {
    expect(getByPath({ items: [1] }, 'items.5')).toBeUndefined();
    expect(getByPath({ items: [1] }, 'items.x')).toBeUndefined();
  });

  it('returns undefined for null row or empty path', () => {
    expect(getByPath(null, 'a')).toBeUndefined();
    expect(getByPath({ a: 1 }, '')).toBeUndefined();
  });
});

describe('formatValue (closed-enum formatters)', () => {
  it('renders null/undefined as an em dash, not 0', () => {
    expect(formatValue(null, 'number')).toBe('—');
    expect(formatValue(undefined, 'currency')).toBe('—');
  });

  it('passes text through as a string', () => {
    expect(formatValue('team-a', 'text')).toBe('team-a');
    expect(formatValue(42, 'text')).toBe('42');
  });

  it('formats currency as USD', () => {
    expect(formatValue(1234.5, 'currency')).toBe('$1,234.50');
  });

  it('formats numbers with grouping and limited fraction digits', () => {
    expect(formatValue(1234567, 'number')).toBe('1,234,567');
    expect(formatValue(3.14159, 'number')).toBe('3.14');
  });

  it('formats bytes via the host helper', () => {
    expect(formatValue(1024, 'bytes')).toBe('1 KiB');
  });

  it('stringifies an object cell instead of leaking [object Object]', () => {
    expect(formatValue({ a: 1 }, 'text')).toBe('{"a":1}');
  });

  it('coerces a non-numeric value passed to a numeric format back to its string', () => {
    expect(formatValue('n/a', 'number')).toBe('n/a');
  });
});

describe('formatDuration', () => {
  it('renders the largest two units from a seconds count', () => {
    expect(formatDuration(3900)).toBe('1h 5m');
    expect(formatDuration(45)).toBe('45s');
    expect(formatDuration(90061)).toBe('1d 1h');
  });

  it('renders 0s for zero and clamps negatives', () => {
    expect(formatDuration(0)).toBe('0s');
    expect(formatDuration(-5)).toBe('0s');
  });
});

describe('extractRows / extractObject', () => {
  it('returns a bare array of record rows', () => {
    expect(extractRows(res([{ a: 1 }, { b: 2 }]))).toEqual([{ a: 1 }, { b: 2 }]);
  });

  it('unwraps a { rows: [...] } envelope', () => {
    expect(extractRows(res({ rows: [{ a: 1 }] }))).toEqual([{ a: 1 }]);
  });

  it('wraps a single object response as one row', () => {
    expect(extractRows(res({ usd: 5 }, 'object'))).toEqual([{ usd: 5 }]);
  });

  it('drops non-record array entries', () => {
    expect(extractRows(res([{ a: 1 }, 7, null]))).toEqual([{ a: 1 }]);
  });

  it('returns [] for undefined / scalar data', () => {
    expect(extractRows(undefined)).toEqual([]);
    expect(extractRows(res(42))).toEqual([]);
  });

  it('extractObject returns the first row or {} when absent', () => {
    expect(extractObject(res([{ a: 1 }]))).toEqual({ a: 1 });
    expect(extractObject(res([]))).toEqual({});
  });
});

describe('buildChartSeries / chartMax', () => {
  const spec: ChartSpec = { type: 'bar', x: 'day', y: ['hits', 'errs'] };

  it('maps rows to { x, values[] } points coercing y to finite numbers', () => {
    const points = buildChartSeries([{ day: 'Mon', hits: 10, errs: 2 }], spec);
    expect(points).toEqual([{ x: 'Mon', values: [10, 2] }]);
  });

  it('coerces a missing / non-numeric y value to 0', () => {
    const points = buildChartSeries([{ day: 'Tue', hits: 'x' }], spec);
    expect(points).toEqual([{ x: 'Tue', values: [0, 0] }]);
  });

  it('chartMax is the max across all series, >= 0', () => {
    const points = buildChartSeries(
      [
        { day: 'Mon', hits: 10, errs: 2 },
        { day: 'Tue', hits: 4, errs: 30 },
      ],
      spec,
    );
    expect(chartMax(points)).toBe(30);
    expect(chartMax([])).toBe(0);
  });
});

describe('isEmptyResponse', () => {
  it('treats undefined and empty lists as empty', () => {
    expect(isEmptyResponse(undefined, 'list')).toBe(true);
    expect(isEmptyResponse(res([]), 'list')).toBe(true);
  });

  it('treats a non-empty list as not empty', () => {
    expect(isEmptyResponse(res([{ a: 1 }]), 'list')).toBe(false);
  });

  it('treats an empty object as empty for the object shape', () => {
    expect(isEmptyResponse(res({}, 'object'), 'object')).toBe(true);
    expect(isEmptyResponse(res({ a: 1 }, 'object'), 'object')).toBe(false);
  });
});

describe('tableColumns', () => {
  it('uses explicit field bindings when given', () => {
    const cols = tableColumns([{ a: 1 }], [{ path: 'a', label: 'A', format: 'number' }]);
    expect(cols).toEqual([{ path: 'a', label: 'A', format: 'number' }]);
  });

  it('falls back to the first row keys as text columns', () => {
    const cols = tableColumns([{ ns: 'x', usd: 1 }]);
    expect(cols).toEqual([
      { path: 'ns', label: 'ns', format: 'text' },
      { path: 'usd', label: 'usd', format: 'text' },
    ]);
  });

  it('returns no columns for empty rows with no fields', () => {
    expect(tableColumns([])).toEqual([]);
  });
});
