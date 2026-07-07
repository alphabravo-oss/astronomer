import api from '@/lib/api';
import {
  listGatekeeperConstraints,
  validateGatekeeperConstraint,
  applyGatekeeperConstraint,
  deleteGatekeeperConstraint,
} from './gatekeeper-constraints';

jest.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: jest.fn(),
    post: jest.fn(),
    delete: jest.fn(),
  },
}));

const mockedApi = api as jest.Mocked<typeof api>;

describe('gatekeeper constraints API client', () => {
  beforeEach(() => jest.clearAllMocks());

  it('unwraps the { data: { items } } list envelope', async () => {
    mockedApi.get.mockResolvedValueOnce({
      data: { data: { items: [{ name: 'c1', kind: 'K8sRequiredLabels', source: 'custom', violationCount: 2 }] } },
    });
    await expect(listGatekeeperConstraints('cl1')).resolves.toEqual([
      expect.objectContaining({ name: 'c1', violationCount: 2 }),
    ]);
    expect(mockedApi.get).toHaveBeenCalledWith('/clusters/cl1/gatekeeper/constraints/');
  });

  it('posts YAML to the validate endpoint (no apply)', async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: { valid: true, errors: [], applied: false, name: 'c1', kind: 'K8sRequiredLabels' },
    });
    await expect(validateGatekeeperConstraint('cl1', 'yaml')).resolves.toEqual(
      expect.objectContaining({ valid: true, applied: false }),
    );
    expect(mockedApi.post).toHaveBeenCalledWith('/clusters/cl1/gatekeeper/constraints/validate/', {
      yaml: 'yaml',
    });
  });

  it('unwraps a { data }-wrapped validate/apply response defensively', async () => {
    mockedApi.post.mockResolvedValueOnce({
      data: { data: { valid: true, errors: [], applied: true, name: 'c1', kind: 'K8sRequiredLabels' } },
    });
    await expect(applyGatekeeperConstraint('cl1', 'yaml')).resolves.toEqual(
      expect.objectContaining({ applied: true }),
    );
    expect(mockedApi.post).toHaveBeenCalledWith('/clusters/cl1/gatekeeper/constraints/', {
      yaml: 'yaml',
    });
  });

  it('url-encodes the constraint name on delete', async () => {
    mockedApi.delete.mockResolvedValueOnce({});
    await deleteGatekeeperConstraint('cl1', 'require labels');
    expect(mockedApi.delete).toHaveBeenCalledWith(
      '/clusters/cl1/gatekeeper/constraints/require%20labels/',
    );
  });
});
