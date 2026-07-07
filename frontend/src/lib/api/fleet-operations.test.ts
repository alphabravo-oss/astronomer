import api from '@/lib/api';
import {
  getFleetOperations,
  getFleetOperation,
  createFleetOperation,
  getFleetTargets,
  pauseFleetOperation,
  resumeFleetOperation,
  abortFleetOperation,
  retryFailedFleetOperation,
  selectorIsEmpty,
  matchesFleetSelector,
  evaluateFleetSelector,
  isTerminalFleetStatus,
  type FleetSelector,
  type SelectorCandidate,
} from './fleet-operations';

jest.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: jest.fn(),
    post: jest.fn(),
  },
}));

const mockedApi = api as jest.Mocked<typeof api>;

describe('fleet-operations API client', () => {
  beforeEach(() => jest.clearAllMocks());

  it('GET list hits the trailing-slash route and passes pagination/status params', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: [], count: 0, next: null, previous: null } });
    await getFleetOperations({ status: 'running', limit: 50, offset: 10 });
    expect(mockedApi.get).toHaveBeenCalledWith('/fleet-operations/', {
      params: { status: 'running', limit: 50, offset: 10 },
    });
  });

  it('GET detail unwraps the { data } envelope', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: { id: 'op1', name: 'x' } } });
    await expect(getFleetOperation('op1')).resolves.toEqual(
      expect.objectContaining({ id: 'op1' }),
    );
    expect(mockedApi.get).toHaveBeenCalledWith('/fleet-operations/op1/');
  });

  it('POST create unwraps the { data } envelope and posts the body', async () => {
    const body = {
      name: 'Upgrade',
      operation_type: 'tool_upgrade' as const,
      selector: { matchLabels: { tier: 'prod' } },
    };
    mockedApi.post.mockResolvedValueOnce({ data: { data: { id: 'op2' } } });
    await expect(createFleetOperation(body)).resolves.toEqual(
      expect.objectContaining({ id: 'op2' }),
    );
    expect(mockedApi.post).toHaveBeenCalledWith('/fleet-operations/', body);
  });

  it('GET targets hits the nested trailing-slash route', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: [], count: 0, next: null, previous: null } });
    await getFleetTargets('op1', { limit: 200 });
    expect(mockedApi.get).toHaveBeenCalledWith('/fleet-operations/op1/targets/', {
      params: { limit: 200 },
    });
  });

  it.each([
    ['pause', pauseFleetOperation, '/fleet-operations/op1/pause/'],
    ['resume', resumeFleetOperation, '/fleet-operations/op1/resume/'],
    ['abort', abortFleetOperation, '/fleet-operations/op1/abort/'],
    ['retry-failed', retryFailedFleetOperation, '/fleet-operations/op1/retry-failed/'],
  ] as const)('POST %s hits the trailing-slash lifecycle route', async (_name, fn, url) => {
    mockedApi.post.mockResolvedValueOnce({ data: { data: { id: 'op1' } } });
    await fn('op1');
    expect(mockedApi.post).toHaveBeenCalledWith(url);
  });
});

describe('selectorIsEmpty', () => {
  it('treats null / {} as empty', () => {
    expect(selectorIsEmpty(null)).toBe(true);
    expect(selectorIsEmpty({})).toBe(true);
    expect(selectorIsEmpty({ matchLabels: {} })).toBe(true);
    expect(selectorIsEmpty({ matchExpressions: [] })).toBe(true);
    expect(selectorIsEmpty({ matchGroupIDs: [] })).toBe(true);
  });

  it('treats any populated field as non-empty', () => {
    expect(selectorIsEmpty({ matchLabels: { a: 'b' } })).toBe(false);
    expect(selectorIsEmpty({ matchExpressions: [{ key: 'k', operator: 'Exists' }] })).toBe(false);
    expect(selectorIsEmpty({ matchGroupIDs: ['g1'] })).toBe(false);
  });
});

describe('matchesFleetSelector — mirrors Go matchesSelector', () => {
  const cluster = (labels: Record<string, string>, groupIds?: string[]): SelectorCandidate => ({
    labels,
    groupIds,
  });

  it('matchLabels is AND of equals', () => {
    const sel: FleetSelector = { matchLabels: { tier: 'prod', region: 'us-east' } };
    expect(matchesFleetSelector(sel, cluster({ tier: 'prod', region: 'us-east' }))).toBe(true);
    expect(matchesFleetSelector(sel, cluster({ tier: 'prod', region: 'us-west' }))).toBe(false);
    expect(matchesFleetSelector(sel, cluster({ tier: 'prod' }))).toBe(false);
  });

  it('In matches present-and-listed; misses when absent or not listed', () => {
    const sel: FleetSelector = { matchExpressions: [{ key: 'env', operator: 'In', values: ['staging', 'canary'] }] };
    expect(matchesFleetSelector(sel, cluster({ env: 'staging' }))).toBe(true);
    expect(matchesFleetSelector(sel, cluster({ env: 'prod' }))).toBe(false);
    expect(matchesFleetSelector(sel, cluster({}))).toBe(false);
  });

  it('NotIn matches when absent (k8s semantics) or not listed', () => {
    const sel: FleetSelector = { matchExpressions: [{ key: 'env', operator: 'NotIn', values: ['prod'] }] };
    expect(matchesFleetSelector(sel, cluster({ env: 'staging' }))).toBe(true);
    expect(matchesFleetSelector(sel, cluster({}))).toBe(true);
    expect(matchesFleetSelector(sel, cluster({ env: 'prod' }))).toBe(false);
  });

  it('Exists / DoesNotExist gate on key presence', () => {
    const exists: FleetSelector = { matchExpressions: [{ key: 'gpu', operator: 'Exists' }] };
    expect(matchesFleetSelector(exists, cluster({ gpu: 'true' }))).toBe(true);
    expect(matchesFleetSelector(exists, cluster({}))).toBe(false);
    const notExists: FleetSelector = { matchExpressions: [{ key: 'gpu', operator: 'DoesNotExist' }] };
    expect(matchesFleetSelector(notExists, cluster({}))).toBe(true);
    expect(matchesFleetSelector(notExists, cluster({ gpu: 'true' }))).toBe(false);
  });

  it('unknown operator matches nothing', () => {
    const sel = { matchExpressions: [{ key: 'k', operator: 'EqualsIgnoreCase', values: ['v'] }] } as unknown as FleetSelector;
    expect(matchesFleetSelector(sel, cluster({ k: 'v' }))).toBe(false);
  });

  it('matchGroupIDs matches on intersection', () => {
    const sel: FleetSelector = { matchGroupIDs: ['g1', 'g2'] };
    expect(matchesFleetSelector(sel, cluster({}, ['g2']))).toBe(true);
    expect(matchesFleetSelector(sel, cluster({}, ['g3']))).toBe(false);
    expect(matchesFleetSelector(sel, cluster({}, []))).toBe(false);
  });
});

describe('evaluateFleetSelector', () => {
  it('empty selector matches NO clusters', () => {
    const candidates: SelectorCandidate[] = [{ labels: { tier: 'prod' } }, { labels: {} }];
    expect(evaluateFleetSelector({}, candidates)).toEqual([]);
  });

  it('filters candidates by the predicate', () => {
    const candidates: SelectorCandidate[] = [
      { labels: { tier: 'prod' } },
      { labels: { tier: 'dev' } },
    ];
    expect(evaluateFleetSelector({ matchLabels: { tier: 'prod' } }, candidates)).toHaveLength(1);
  });
});

describe('isTerminalFleetStatus', () => {
  it('is terminal for completed/failed/aborted only', () => {
    expect(isTerminalFleetStatus('completed')).toBe(true);
    expect(isTerminalFleetStatus('failed')).toBe(true);
    expect(isTerminalFleetStatus('aborted')).toBe(true);
    expect(isTerminalFleetStatus('running')).toBe(false);
    expect(isTerminalFleetStatus('paused')).toBe(false);
    expect(isTerminalFleetStatus('pending')).toBe(false);
  });
});
