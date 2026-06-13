import fs from 'node:fs';
import path from 'node:path';
import yaml from 'js-yaml';

type OpenAPIDocument = {
  paths?: Record<string, Record<string, unknown>>;
};

const contractPaths: Array<{ method: string; path: string }> = [
  { method: 'post', path: '/api/v1/auth/login/' },
  { method: 'post', path: '/api/v1/auth/logout/' },
  { method: 'get', path: '/api/v1/auth/me/' },
  { method: 'get', path: '/api/v1/settings/features/' },
  { method: 'get', path: '/api/v1/clusters/' },
  { method: 'post', path: '/api/v1/clusters/' },
  { method: 'get', path: '/api/v1/clusters/{id}/' },
  { method: 'put', path: '/api/v1/clusters/{id}/registration/options/' },
  { method: 'get', path: '/api/v1/activity/' },
  { method: 'get', path: '/api/v1/alerting/events/' },
  { method: 'get', path: '/api/v1/tools/' },
  { method: 'get', path: '/api/v1/argocd/instances/' },
  { method: 'get', path: '/api/v1/admin/backup-drill/' },
];

describe('frontend API contract', () => {
  const docPath = path.resolve(__dirname, '../../../../docs/openapi.yaml');
  const doc = yaml.load(fs.readFileSync(docPath, 'utf8')) as OpenAPIDocument;

  it.each(contractPaths)('$method $path is documented in OpenAPI', ({ method, path: apiPath }) => {
    expect(doc.paths?.[apiPath]?.[method]).toBeDefined();
  });
});
