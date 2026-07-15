import type { Mocked } from 'vitest';
import api from '../api';
import { listDLQ, listQueues } from './admin-operations';

vi.mock('../api', () => ({
  __esModule: true,
  default: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
}));

const mockedApi = api as Mocked<typeof api>;

describe('admin operations API client', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('unwraps standard API envelopes for queue lists', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: [
          {
            name: 'default',
            size: 1,
            active: 0,
            pending: 1,
            scheduled: 0,
            retry: 0,
            archived: 0,
            completed: 0,
            paused: false,
            as_of: '2026-06-15T00:00:00Z',
          },
        ],
      },
    });

    await expect(listQueues()).resolves.toEqual([
      expect.objectContaining({ name: 'default', pending: 1 }),
    ]);
  });

  it('keeps raw queue array compatibility', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: [
        {
          name: 'tunnel',
          size: 0,
          active: 0,
          pending: 0,
          scheduled: 0,
          retry: 0,
          archived: 0,
          completed: 0,
          paused: false,
          as_of: '2026-06-15T00:00:00Z',
        },
      ],
    });

    await expect(listQueues()).resolves.toEqual([
      expect.objectContaining({ name: 'tunnel' }),
    ]);
  });

  it('unwraps standard API envelopes for DLQ reads', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: {
        data: {
          queue: 'default',
          dlq: [],
          count: 0,
        },
      },
    });

    await expect(listDLQ('default')).resolves.toEqual({
      queue: 'default',
      dlq: [],
      count: 0,
    });
  });
});
