import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { usesGeneratedSchemaReference } from './generated-schema-binding.mjs';
test('canonical and read schema namespaces are bound to actual generated imports', () => {
  assert.equal(usesGeneratedSchemaReference('type View = OpenAPIComponents["schemas"]["Cluster"];', 'import type { OpenAPIComponents } from "@/types/openapi.generated";'), true);
  assert.equal(usesGeneratedSchemaReference('type View = StrictComponents["schemas"]["Cluster"];', 'import type { OpenAPIComponents as StrictComponents } from "@/types/openapi.generated";'), true);
  assert.equal(usesGeneratedSchemaReference('type View = ReadComponents["schemas"]["Cluster"];', 'import type { OpenAPIReadComponents as ReadComponents } from "@/types/categories.generated";'), true);
});
test('copied DTO shapes, invented identifiers and unrelated imports cannot evade shadow detection', () => {
  assert.equal(usesGeneratedSchemaReference('type View = { status: string };', 'import type { OpenAPIComponents } from "@/types/openapi.generated";'), false);
  assert.equal(usesGeneratedSchemaReference('type View = StrictComponents["schemas"]["Cluster"];', ''), false);
  assert.equal(usesGeneratedSchemaReference('type View = StrictComponents["schemas"]["Cluster"];', 'import type { OpenAPIComponents as StrictComponents } from "./fake";'), false);
  assert.equal(usesGeneratedSchemaReference('type View = OpenAPIComponents["schemas"]["Cluster"];', 'import type { CopiedShape as OpenAPIComponents } from "@/types/openapi.generated";'), false);
});
