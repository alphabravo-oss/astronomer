import api from '@/lib/api';
import { getQuotaUsage, listSentEmails, listWebhooks } from './settings';

jest.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: jest.fn(),
    post: jest.fn(),
    put: jest.fn(),
    delete: jest.fn(),
  },
}));

const mockedApi = api as jest.Mocked<typeof api>;

describe('settings API client', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('normalizes webhooks items envelopes into arrays', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: {
          items: [
            {
              id: 'webhook-1',
              name: 'Ops',
              url: 'https://hooks.example.com',
              template: 'generic',
              secret: '__redacted__',
              enabled: true,
              filters: { events: [] },
              createdAt: '2026-06-15T00:00:00Z',
              updatedAt: '2026-06-15T00:00:00Z',
            },
          ],
          total: 1,
        },
      },
    });

    await expect(listWebhooks()).resolves.toEqual([
      expect.objectContaining({ id: 'webhook-1', name: 'Ops' }),
    ]);
  });

  it('normalizes sent email items envelopes into paginated responses', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: {
          items: [],
          limit: 25,
          offset: 25,
          total: 60,
        },
      },
    });

    await expect(listSentEmails({ page: 2, page_size: 25 })).resolves.toEqual(
      expect.objectContaining({
        data: [],
        page: 2,
        pageSize: 25,
        total: 60,
        totalPages: 3,
      }),
    );
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/emails', {
      params: { limit: 25, offset: 25, status: undefined },
    });
  });

  it('normalizes fleet quota usage snapshots for the existing table UI', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: {
          global: {
            totalClusters: 2,
            maxTotalClusters: 10,
            totalUsers: 7,
            maxTotalUsers: 20,
          },
          projectOffenders: [
            {
              projectId: 'project-1',
              projectName: 'Platform',
              quotaPlan: 'default',
              limit: 'max_cpu_cores',
              current: 90,
              maximum: 100,
              usagePct: 90,
            },
          ],
          userOffenders: [
            {
              userId: 'user-1',
              username: 'admin@alphabravo.io',
              quotaPlan: 'global',
              limit: 'max_api_tokens',
              current: 9,
              maximum: 10,
              usagePct: 90,
            },
          ],
        },
      },
    });

    await expect(getQuotaUsage()).resolves.toEqual({
      fleetTotals: { max_clusters: 2, max_users: 7 },
      rows: [
        expect.objectContaining({ scope: 'project', scopeName: 'Platform' }),
        expect.objectContaining({ scope: 'user', scopeName: 'admin@alphabravo.io' }),
      ],
      topOffenders: [
        expect.objectContaining({
          planName: 'default',
          usage: { max_cpu_cores: 90 },
          utilization: { max_cpu_cores: 90 },
        }),
        expect.objectContaining({
          planName: 'global',
          usage: { max_api_tokens: 9 },
          utilization: { max_api_tokens: 90 },
        }),
      ],
    });
  });
});
