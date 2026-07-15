import type { Mocked } from 'vitest';
import api from '@/lib/api';
import {
  getExtensionMounts,
  fetchExtensionData,
  requestExtensionBridgeToken,
} from './extensions';

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

describe('extensions host-runtime API client', () => {
  beforeEach(() => vi.clearAllMocks());

  describe('getExtensionMounts', () => {
    it('hits the viewer-readable /extensions/mounts/ endpoint (no /api/v1 prefix)', async () => {
      mockedApi.get.mockResolvedValueOnce({
        data: { data: { sidebar: [], dashboardWidgets: [], clusterTabs: [], settings: [] } },
      } as never);

      await getExtensionMounts();

      expect(mockedApi.get).toHaveBeenCalledWith('/extensions/mounts/');
    });

    it('backfills missing buckets to empty arrays so callers never null-check', async () => {
      // Server returns only one bucket populated; the rest absent.
      mockedApi.get.mockResolvedValueOnce({
        data: { data: { clusterTabs: [{ extension: 'x' }] } },
      } as never);

      const res = await getExtensionMounts();

      expect(res.sidebar).toEqual([]);
      expect(res.dashboardWidgets).toEqual([]);
      expect(res.settings).toEqual([]);
      expect(res.clusterTabs).toHaveLength(1);
    });

    it('tolerates a null data envelope', async () => {
      mockedApi.get.mockResolvedValueOnce({ data: { data: null } } as never);

      const res = await getExtensionMounts();

      expect(res).toEqual({ sidebar: [], dashboardWidgets: [], clusterTabs: [], settings: [] });
    });
  });

  describe('fetchExtensionData', () => {
    it('POSTs to the name+dataSource data-proxy route, URL-encoding both segments', async () => {
      mockedApi.post.mockResolvedValueOnce({
        data: { data: { data: { rows: [] }, shape: 'list', meta: { dataSourceId: 'pod cost' } } },
      } as never);

      const req = { context: { clusterId: 'c1' }, query: { window: '7d' } };
      const out = await fetchExtensionData('cost insights', 'pod cost', req);

      expect(mockedApi.post).toHaveBeenCalledWith(
        '/extensions/cost%20insights/data/pod%20cost/',
        req,
      );
      expect(out.shape).toBe('list');
    });

    it('defaults to an empty request body when none is supplied', async () => {
      mockedApi.post.mockResolvedValueOnce({
        data: { data: { data: {}, shape: 'object', meta: { dataSourceId: 'd1' } } },
      } as never);

      await fetchExtensionData('ext', 'd1');

      expect(mockedApi.post).toHaveBeenCalledWith('/extensions/ext/data/d1/', {});
    });
  });

  describe('requestExtensionBridgeToken', () => {
    it('POSTs the dataSource + context to the ticket-issuance route', async () => {
      mockedApi.post.mockResolvedValueOnce({
        data: {
          data: {
            token: 'opaque',
            dataSource: 'podCost',
            expiresAt: '2026-06-25T12:00:60Z',
            scope: 'ext:cost-insights:data:podCost',
          },
        },
      } as never);

      const ctx = { clusterId: 'c1' };
      const tok = await requestExtensionBridgeToken('cost-insights', 'podCost', ctx);

      expect(mockedApi.post).toHaveBeenCalledWith('/extensions/cost-insights/token/', {
        dataSource: 'podCost',
        context: ctx,
      });
      expect(tok.token).toBe('opaque');
      expect(tok.scope).toBe('ext:cost-insights:data:podCost');
    });
  });
});
