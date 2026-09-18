import assert from "node:assert/strict";
import { test } from "node:test";
import { render, renderEnvironment } from "./generate-config-docs.mjs";

const source = `
 Enabled bool \`mapstructure:"enabled"\`
 Secret string \`mapstructure:"secret"\`
 Count int \`mapstructure:"count"\`
 Mapping string \`mapstructure:"mapping"\`
 envconfig.Default{Key: "enabled", Value: true}
 envconfig.Default{Key: "count", Value: 12}
 envconfig.Default{Key: "mapping", Value: "{}"}
 envconfig.BindEnv(v, "secret"); err
`;
const vite = `process.env.BACKEND_URL ?? 'http://localhost:8001'`;

test("environment keys and defaults follow the configuration schema", () => {
  const output = renderEnvironment(source, 'os.Getenv("LISTEN_ADDR")', vite);
  assert.match(output, /ENABLED="true"\nSECRET=\nCOUNT="12"\nMAPPING="{}"/);
  assert.match(output, /LISTEN_ADDR=\n/);
  assert.match(output, /BACKEND_URL="http:\/\/localhost:8001"/);
  assert.doesNotMatch(output, /CELERY|NEXTAUTH|POSTGRES_PORT/);
});

test("schema drift cannot silently drop a bound field or default", () => {
  assert.throws(
    () => render(source + '\nLost string `mapstructure:"lost"`'),
    /neither an environment binding/,
  );
  assert.throws(
    () => render(source + '\nenvconfig.BindEnv(v, "missing"); err'),
    /binding missing/,
  );
  assert.throws(
    () => render(source + '\nenvconfig.Default{Key: "missing", Value: true}'),
    /default missing/,
  );
  assert.throws(
    () => render(source + '\nAgain bool `mapstructure:"enabled"`'),
    /duplicate mapstructure/,
  );
});

test("unrecognized defaults and missing Vite configuration fail closed", () => {
  assert.throws(
    () =>
      renderEnvironment(
        source.replace("Value: 12", "Value: newDefault()"),
        "",
        vite,
      ),
    /unsupported environment default/,
  );
  assert.throws(() => renderEnvironment(source, "", ""), /BACKEND_URL/);
});
