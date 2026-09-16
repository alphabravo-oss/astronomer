import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

// Tailwind resolves colour classes at build time from the CSS-first theme; a
// class naming a colour that does not exist is silently dropped, so the element
// just renders uncoloured. Nothing in tsc, eslint or the build catches it, so
// assert every `<utility>-status-<name>` names a registered v4 theme colour.
const SRC = join(__dirname, "..");
const CLASS_RE =
  /\b(?:bg|text|border|ring|fill|stroke|from|to|via)-status-([a-z]+)\b/g;

function walk(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === "node_modules" ? [] : walk(p);
    return /\.tsx?$/.test(e.name) ? [p] : [];
  });
}

function statusThemeColors(): Set<string> {
  const css = readFileSync(join(SRC, "styles", "globals.css"), "utf8");
  return new Set(
    Array.from(css.matchAll(/--color-status-([a-z]+):/g), (match) => match[1]),
  );
}

describe("status colour palette", () => {
  it("defines every status colour referenced by a class in src", () => {
    const defined = statusThemeColors();
    expect(defined.size).toBeGreaterThan(0);

    const offenders: string[] = [];
    for (const file of walk(SRC)) {
      const text = readFileSync(file, "utf8");
      for (const [cls, color] of text.matchAll(CLASS_RE)) {
        if (!defined.has(color))
          offenders.push(`${file.slice(SRC.length + 1)}: ${cls}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
