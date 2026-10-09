// Recognize actual generated-schema imports, including qualified read representations.
export function usesGeneratedSchemaReference(declaration, source) {
  for (const match of source.matchAll(/import\s+type\s*\{([^}]+)\}\s*from\s*["']@\/types\/(?:openapi|categories)\.generated["']/g)) {
    for (const item of match[1].split(',')) {
      const binding = /^(OpenAPIComponents|OpenAPIReadComponents)(?:\s+as\s+([A-Za-z][A-Za-z0-9_]*))?$/.exec(item.trim());
      if (binding && new RegExp(`\\b${binding[2] ?? binding[1]}\\b`).test(declaration)) return true;
    }
  }
  return false;
}
