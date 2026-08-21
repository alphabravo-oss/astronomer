import {
  auditFilterChips,
  buildAuditQuery,
  clearFilterValue,
  countActiveFilters,
  countAdvancedFilters,
  emptyFilters,
} from './-filters';

describe('audit filters', () => {
  it('counts only set values', () => {
    expect(countActiveFilters(emptyFilters)).toBe(0);
    expect(
      countActiveFilters({
        ...emptyFilters,
        q: 'login',
        actionClass: 'auth',
        clusterId: 'c1',
      }),
    ).toBe(3);
    expect(
      countAdvancedFilters({
        ...emptyFilters,
        q: 'login',
        actionClass: 'auth',
        clusterId: 'c1',
      }),
    ).toBe(1);
  });

  it('sends q and omits default class/result', () => {
    const query = buildAuditQuery({ ...emptyFilters, q: '  login  ', result: 'failure' }, 2);
    expect(query).toMatchObject({
      q: 'login',
      audience: 'people',
      result: 'failure',
      limit: 50,
      offset: 100,
    });
    expect(query.action_class).toBeUndefined();
  });

  it('does not treat the people audience as an extra filter', () => {
    expect(countActiveFilters(emptyFilters)).toBe(0);
    expect(countActiveFilters({ ...emptyFilters, audience: 'system' })).toBe(1);
  });

  it('renders chips with cluster names', () => {
    const chips = auditFilterChips(
      { ...emptyFilters, q: 'login', clusterId: 'abc' },
      { clusters: { abc: 'local' } },
    );
    expect(chips.map((c) => c.label)).toEqual(['Search: login', 'Cluster: local']);
  });

  it('resets class/result to all', () => {
    expect(clearFilterValue('actionClass')).toBe('all');
    expect(clearFilterValue('q')).toBe('');
  });
});
