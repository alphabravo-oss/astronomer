import api from '../api';
import { listDLQ, listQueues } from './admin-operations';

jest.mock('../api', () => ({
  __esModule: true,
  default: {
    get: jest.fn(),
    post: jest.fn(),
    delete: jest.fn(),
  },
}));

const mockedApi = api as jest.Mocked<typeof api>;

describe('admin operations API client', () => {
  beforeEach(() => {
    jest.clearAllMocks();
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
