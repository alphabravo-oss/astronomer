import type { Mocked } from 'vitest';
import api from '@/lib/api';
import { addProjectNamespace, removeProjectNamespace } from './project-detail';

vi.mock('@/lib/api', () => ({
  __esModule: true,
  default: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

const mockedApi = api as Mocked<typeof api>;

/**
 * The project→namespace endpoints are the only authoring surface for project
 * tenancy, and a project's namespaces are what its role bindings resolve to. The
 * component test stubs the whole hooks module, so without these the request
 * shape is never exercised anywhere: a path typo or a changed method would ship
 * green.
 *
 * The chi routes are registered as `/{id}/add-namespace/` and
 * `/{id}/remove-namespace/` WITH a trailing slash; the URLs below omit it and
 * rely on the request interceptor in lib/api.ts appending one (it skips only
 * URLs already ending in `/`, containing `?`, or containing `/k8s/`). That
 * dependency is load-bearing, so it is asserted explicitly here.
 */
describe('project namespace membership API client', () => {
  beforeEach(() => vi.clearAllMocks());

  it('POSTs the namespace to add-namespace and unwraps the project', async () => {
    mockedApi.post.mockResolvedValueOnce({ data: { data: { id: 'p1', namespaces: ['team-a'] } } });

    const project = await addProjectNamespace('p1', 'team-a');

    expect(mockedApi.post).toHaveBeenCalledTimes(1);
    const [url, body] = mockedApi.post.mock.calls[0];
    expect(url).toBe('/projects/p1/add-namespace');
    expect(body).toEqual({ namespace: 'team-a' });
    // No query string and no /k8s/ segment, so the interceptor appends the
    // trailing slash the chi route requires.
    expect(url).not.toContain('?');
    expect(url).not.toContain('/k8s/');
    expect(project).toEqual({ id: 'p1', namespaces: ['team-a'] });
  });

  it('POSTs the namespace to remove-namespace and unwraps the project', async () => {
    mockedApi.post.mockResolvedValueOnce({ data: { data: { id: 'p1', namespaces: [] } } });

    const project = await removeProjectNamespace('p1', 'team-a');

    expect(mockedApi.post).toHaveBeenCalledTimes(1);
    const [url, body] = mockedApi.post.mock.calls[0];
    expect(url).toBe('/projects/p1/remove-namespace');
    expect(body).toEqual({ namespace: 'team-a' });
    expect(url).not.toContain('?');
    expect(url).not.toContain('/k8s/');
    expect(project).toEqual({ id: 'p1', namespaces: [] });
  });
});
