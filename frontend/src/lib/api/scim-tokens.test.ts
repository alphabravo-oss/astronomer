import api from '@/lib/api';
import { listSCIMTokens, createSCIMToken, deleteSCIMToken } from './scim-tokens';

jest.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: jest.fn(),
    post: jest.fn(),
    delete: jest.fn(),
  },
}));

const mockedApi = api as jest.Mocked<typeof api>;

describe('SCIM tokens API client', () => {
  beforeEach(() => jest.clearAllMocks());

  it('unwraps the { data: { tokens } } list envelope', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: { data: { tokens: [{ id: 't1', name: 'okta', prefix: 'astro_scim_ab' }] } },
    });
    await expect(listSCIMTokens()).resolves.toEqual([
      expect.objectContaining({ id: 't1', name: 'okta' }),
    ]);
    expect(mockedApi.get).toHaveBeenCalledWith('/admin/scim-tokens/');
  });

  it('returns an empty array when tokens is absent', async () => {
    mockedApi.get.mockResolvedValueOnce({ data: { data: {} } });
    await expect(listSCIMTokens()).resolves.toEqual([]);
  });

  it('posts the name and returns the one-time plaintext token', async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: { data: { id: 't2', name: 'okta', prefix: 'astro_scim_cd', token: 'astro_scim_secret' } },
    });
    await expect(createSCIMToken('okta')).resolves.toEqual(
      expect.objectContaining({ id: 't2', token: 'astro_scim_secret' }),
    );
    expect(mockedApi.post).toHaveBeenCalledWith('/admin/scim-tokens/', { name: 'okta' });
  });

  it('deletes at the id path', async () => {
    mockedApi.delete.mockResolvedValueOnce({});
    await deleteSCIMToken('t2');
    expect(mockedApi.delete).toHaveBeenCalledWith('/admin/scim-tokens/t2/');
  });
});
