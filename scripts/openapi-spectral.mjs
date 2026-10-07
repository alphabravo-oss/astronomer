#!/usr/bin/env node
// Lints docs/openapi.yaml with the repo's .spectral.yaml ruleset.
//
// This drives @stoplight/spectral-core directly instead of the spectral CLI:
// the CLI pulls fast-glob -> micromatch -> braces, which has an unpatched
// stack-exhaustion advisory and failed the frontend dependency audit. The
// library has no glob dependency and is all this lint needs.

import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(path.join(root, "frontend", "package.json"));
const { Spectral, Document } = require("@stoplight/spectral-core");
const { oas } = require("@stoplight/spectral-rulesets");
const Parsers = require("@stoplight/spectral-parsers");
const yaml = require("js-yaml");

const spec = path.join(root, "docs", "openapi.yaml");
const rulesetFile = path.join(root, ".spectral.yaml");

// Only the built-in OpenAPI ruleset is supported as a base, which is all
// .spectral.yaml extends. Fail loudly if someone adds another.
const config = yaml.load(fs.readFileSync(rulesetFile, "utf8")) ?? {};
const extendsList = [config.extends ?? []].flat();
const unsupported = extendsList.filter((name) => name !== "spectral:oas");
if (unsupported.length > 0) {
  console.error(`OpenAPI Spectral: unsupported ruleset extends: ${unsupported.join(", ")}`);
  process.exit(2);
}

let diagnostics;
try {
  const spectral = new Spectral();
  spectral.setRuleset({
    extends: extendsList.length > 0 ? [oas] : [],
    rules: config.rules ?? {},
  });
  diagnostics = await spectral.run(
    new Document(fs.readFileSync(spec, "utf8"), Parsers.Yaml, spec),
  );
} catch (error) {
  console.error(`OpenAPI Spectral execution failed: ${error.message}`);
  process.exit(2);
}

const errors = diagnostics.filter((diagnostic) => diagnostic.severity === 0);
if (errors.length > 0) {
  console.error(`OpenAPI Spectral schema lint failed with ${errors.length} error(s):`);
  for (const error of errors) {
    const location = error.range?.start
      ? `${error.range.start.line + 1}:${error.range.start.character + 1}`
      : "unknown";
    console.error(`  - ${location} ${error.code}: ${error.message}`);
  }
  process.exit(1);
}
console.log(`OpenAPI Spectral schema lint passed (${diagnostics.length} advisory warning(s) suppressed).`);
