import type { Mocked } from 'vitest';
import api from '@/lib/api';
import {
  listSIEMForwarders,
  getSIEMForwarder,
  createSIEMForwarder,
  updateSIEMForwarder,
  deleteSIEMForwarder,
  testSIEMForwarder,
  getSIEMForwarderStatus,
} from './siem-forwarders';

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

describe('SIEM forwarders API client', () => {
  beforeEach(() => vi.clearAllMocks());

  it('unwraps the { data: { items } } list envelope into an array', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: { data: { items: [{ id: 'f1', name: 'splunk' }], total: 1 } },
    });
    await expect(listSIEMForwarders()).resolves.toEqual([
      expect.objectContaining({ id: 'f1', name: 'splunk' }),
    ]);
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/siem-forwarders/');
  });

  it('returns an empty array when the list envelope is missing items', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: {} } });
    await expect(listSIEMForwarders()).resolves.toEqual([]);
  });

  it('unwraps a single forwarder from { data }', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: { id: 'f1', name: 'splunk' } } });
    await expect(getSIEMForwarder('f1')).resolves.toEqual(
      expect.objectContaining({ id: 'f1' }),
    );
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/siem-forwarders/f1/');
  });

  it('posts the snake_case write body on create', async () => {
    mockedApi.post.mockResolvedValueOnce({ data: { data: { id: 'f2' } } });
    await createSIEMForwarder({ name: 'x', transport: 'syslog_tls', endpoint: 'h:6514', tls_skip_verify: true });
    expect(mockedApi.post).toHaveBeenCalledWith('/admin/siem-forwarders/', {
      name: 'x',
      transport: 'syslog_tls',
      endpoint: 'h:6514',
      tls_skip_verify: true,
    });
  });

  it('puts to the id path on update', async () => {
    mockedApi.put.mockResolvedValueOnce({ data: { data: { id: 'f2' } } });
    await updateSIEMForwarder('f2', { enabled: false });
    expect(mockedApi.put).toHaveBeenCalledWith('/admin/siem-forwarders/f2/', { enabled: false });
  });

  it('deletes at the id path', async () => {
    mockedApi.delete.mockResolvedValueOnce({});
    await deleteSIEMForwarder('f2');
    expect(mockedApi.delete).toHaveBeenCalledWith('/admin/siem-forwarders/f2/');
  });

  it('returns the unwrapped test result verbatim', async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: { queueId: 'q1', forwarderId: 'f1', queuedAt: 't', message: 'ok' },
    });
    await expect(testSIEMForwarder('f1')).resolves.toEqual(
      expect.objectContaining({ queueId: 'q1', message: 'ok' }),
    );
    expect(mockedApi.post).toHaveBeenCalledWith('/admin/siem-forwarders/f1/test/');
  });

  it('returns the unwrapped status body verbatim', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: { forwarderId: 'f1', droppedTotal: 3, dispatchedTotal: 10, queueDepth: 2 },
    });
    await expect(getSIEMForwarderStatus('f1')).resolves.toEqual(
      expect.objectContaining({ droppedTotal: 3, dispatchedTotal: 10 }),
    );
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/siem-forwarders/f1/status/');
  });
});
