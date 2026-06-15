import fs from 'node:fs';
import path from 'node:path';
import yaml from 'js-yaml';

type OpenAPIDocument = {
  paths?: Record<string, Record<string, unknown>>;
  components?: {
    schemas?: Record<string, unknown>;
  };
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
  const embeddedDocPath = path.resolve(
    __dirname,
    '../../../../internal/handler/assets/openapi.yaml',
  );
  const generatedTypesPath = path.resolve(
    __dirname,
    '../../types/openapi.generated.ts',
  );
  const doc = yaml.load(fs.readFileSync(docPath, 'utf8')) as OpenAPIDocument;

  it('keeps the public docs spec identical to the embedded handler asset', () => {
    expect(fs.readFileSync(docPath, 'utf8')).toBe(fs.readFileSync(embeddedDocPath, 'utf8'));
  });

  it.each(contractPaths)('$method $path is documented in OpenAPI', ({ method, path: apiPath }) => {
    expect(doc.paths?.[apiPath]?.[method]).toBeDefined();
  });

  it('has generated frontend wire types for every OpenAPI component schema', () => {
    const generated = fs.readFileSync(generatedTypesPath, 'utf8');
    const schemaNames = Object.keys(doc.components?.schemas ?? {});

    expect(schemaNames.length).toBeGreaterThan(0);
    for (const schemaName of schemaNames) {
      expect(generated).toContain(
        `export type ${schemaName} = OpenAPIComponents['schemas']['${schemaName}'];`,
      );
    }
  });
});
