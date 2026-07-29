/**
 * Request contract for POST /api/v1/catalog/repositories/.
 *
 * The bug: `createHelmRepository` posted its camelCase argument object
 * verbatim. Only RESPONSES are translated (the `camelizeKeys` interceptor);
 * there is no request interceptor, so the server saw `repoType` where it reads
 * `repo_type`, and saw `username`/`password` at the top level where it reads
 * `auth_config`. Go's json decoder discards unknown fields silently, so an
 * operator who filled in the Authentication fields got a repository with no
 * credential at all — surfacing much later as a 401 from the registry that
 * reads as a wrong password rather than a dropped one. The body also omitted
 * `enabled`, which decoded to `false` and excluded the repository from the
 * scheduled sync sweep entirely.
 *
 * These assertions are on the BODY, not on the arguments, because the body is
 * the half of the contract the compiler cannot see.
 */
import api from '@/lib/api';
import { createHelmRepository } from '@/lib/api';

describe('createHelmRepository request body', () => {
  let post: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    post = vi.spyOn(api, 'post').mockResolvedValue({ data: { data: { id: 'r1' } } });
  });
  afterEach(() => post.mockRestore());

  const bodyOf = () => post.mock.calls[0][1] as Record<string, unknown>;

  it('sends credentials inside auth_config, never at the top level', async () => {
    await createHelmRepository({
      name: 'private',
      url: 'https://charts.example.com',
      repoType: 'helm',
      username: 'deploy',
      password: 's3cret',
    });

    const body = bodyOf();
    expect(body.auth_config).toEqual({ username: 'deploy', password: 's3cret' });
    expect(body).not.toHaveProperty('username');
    expect(body).not.toHaveProperty('password');
  });

  it('sets auth_type so the sync path actually sends the credential', async () => {
    await createHelmRepository({
      name: 'private',
      url: 'https://charts.example.com',
      repoType: 'helm',
      username: 'deploy',
      password: 's3cret',
    });
    // ApplyIndexAuth short-circuits on 'none'/'': a credential stored without
    // an auth_type is a credential that is never sent.
    expect(bodyOf().auth_type).toBe('basic');
  });

  it("sends snake_case repo_type, so the operator's choice is not discarded", async () => {
    await createHelmRepository({ name: 'oci-repo', url: 'oci://registry.example.com', repoType: 'oci' });
    const body = bodyOf();
    expect(body.repo_type).toBe('oci');
    expect(body).not.toHaveProperty('repoType');
  });

  it('enables the repository, so the scheduled sweep picks it up', async () => {
    await createHelmRepository({ name: 'public', url: 'https://charts.example.com', repoType: 'helm' });
    expect(bodyOf().enabled).toBe(true);
  });

  it('sends auth_type none and an empty auth_config for an anonymous repository', async () => {
    await createHelmRepository({ name: 'public', url: 'https://charts.example.com', repoType: 'helm' });
    const body = bodyOf();
    expect(body.auth_type).toBe('none');
    expect(body.auth_config).toEqual({});
  });
});
