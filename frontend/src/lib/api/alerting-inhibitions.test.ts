import type { Mocked } from 'vitest';
import api from '@/lib/api';
import {
  listInhibitions,
  getInhibition,
  createInhibition,
  updateInhibition,
  deleteInhibition,
  toInhibitionWriteRequest,
} from './alerting-inhibitions';

vi.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const mockedApi = api as Mocked<typeof api>;

describe('alerting inhibitions API client', () => {
  beforeEach(() => vi.clearAllMocks());

  it('maps camelCase form matchers to snake_case is_regex on write', () => {
    const body = toInhibitionWriteRequest({
      name: 'suppress-node',
      enabled: true,
      sourceMatchers: [{ label: 'alertname', value: 'ClusterDown', isRegex: false }],
      targetMatchers: [{ label: 'severity', value: 'warn.*', isRegex: true }],
      equalLabels: ['cluster'],
    });
    expect(body).toEqual({
      name: 'suppress-node',
      enabled: true,
      source_matchers: [{ label: 'alertname', value: 'ClusterDown', is_regex: false }],
      target_matchers: [{ label: 'severity', value: 'warn.*', is_regex: true }],
      equal_labels: ['cluster'],
    });
  });

  it('unwraps the { data } list envelope', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: [{ id: 'i1', name: 'r' }] } });
    await expect(listInhibitions()).resolves.toEqual([
      expect.objectContaining({ id: 'i1' }),
    ]);
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/alerting/inhibitions/');
  });

  it('gets a single inhibition by id', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: { id: 'i1' } } });
    await expect(getInhibition('i1')).resolves.toEqual(expect.objectContaining({ id: 'i1' }));
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/alerting/inhibitions/i1/');
  });

  it('posts the write body on create', async () => {
    mockedApi.post.mockResolvedValueOnce({ data: { data: { id: 'i2' } } });
    const body = {
      name: 'r',
      source_matchers: [],
      target_matchers: [],
      equal_labels: [],
      enabled: true,
    };
    await createInhibition(body);
    expect(mockedApi.post).toHaveBeenCalledWith('/admin/alerting/inhibitions/', body);
  });

  it('puts to the id path on update', async () => {
    mockedApi.put.mockResolvedValueOnce({ data: { data: { id: 'i2' } } });
    const body = {
      name: 'r',
      source_matchers: [],
      target_matchers: [],
      equal_labels: [],
      enabled: false,
    };
    await updateInhibition('i2', body);
    expect(mockedApi.put).toHaveBeenCalledWith('/admin/alerting/inhibitions/i2/', body);
  });

  it('deletes at the id path', async () => {
    mockedApi.delete.mockResolvedValueOnce({});
    await deleteInhibition('i2');
    expect(mockedApi.delete).toHaveBeenCalledWith('/admin/alerting/inhibitions/i2/');
  });
});
