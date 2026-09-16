import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const routeParamCast = /\bparams\.[A-Za-z_$][\w$]*\s+as\s+string\b/;

function productionSourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return productionSourceFiles(path);
    return /\.(ts|tsx)$/.test(entry.name) && !/\.(test|spec)\.[jt]sx?$/.test(entry.name)
      ? [path]
      : [];
  });
}

describe("typed route parameters", () => {
  it("does not permit string casts on router params", () => {
    const offenders = productionSourceFiles(join(process.cwd(), "src"))
      .filter((path) => routeParamCast.test(readFileSync(path, "utf8")))
      .map((path) => path.replace(`${process.cwd()}/`, ""));

    expect(offenders).toEqual([]);
  });
});
