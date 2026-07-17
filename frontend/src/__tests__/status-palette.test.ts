import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

// Tailwind resolves colour classes at build time from the palette; a class
// naming a colour that does not exist is silently dropped, so the element just
// renders uncoloured. `status-danger` (30 uses), `status-critical` and
// `status-active` all shipped that way — failure states rendered with no colour
// at all. Nothing in tsc, eslint or the build catches it, so assert it here:
// every `<utility>-status-<name>` must name a colour in the palette.
const SRC = join(__dirname, '..');
const CLASS_RE = /\b(?:bg|text|border|ring|fill|stroke|from|to|via)-status-([a-z]+)\b/g;

function walk(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === 'node_modules' ? [] : walk(p);
    return /\.tsx?$/.test(e.name) ? [p] : [];
  });
}

function paletteColors(): Set<string> {
  const cfg = readFileSync(join(SRC, '..', 'tailwind.config.ts'), 'utf8');
  const block = /status:\s*\{([^}]*)\}/.exec(cfg);
  if (!block) throw new Error('no `status` palette block in tailwind.config.ts');
  return new Set(Array.from(block[1].matchAll(/([a-z]+):\s*'/g), (m) => m[1]));
}

describe('status colour palette', () => {
  it('defines every status colour referenced by a class in src', () => {
    const defined = paletteColors();
    expect(defined.size).toBeGreaterThan(0);

    const offenders: string[] = [];
    for (const file of walk(SRC)) {
      const text = readFileSync(file, 'utf8');
      for (const [cls, color] of text.matchAll(CLASS_RE)) {
        if (!defined.has(color)) offenders.push(`${file.slice(SRC.length + 1)}: ${cls}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
