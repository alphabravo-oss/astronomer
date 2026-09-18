import assert from "node:assert/strict";
import test from "node:test";

import { findNodeVersionDrift } from "./check-node-version-alignment.mjs";

const aligned = {
  version: "24.21.0",
  packageMetadata: { engines: { node: ">=24.21.0 <25" } },
  dockerfile: "FROM node:24-alpine@sha256:abc AS build",
  workflows: [
    ["ci.yaml", "uses: actions/setup-node@sha\nwith:\n  node-version: 24.21.0"],
  ],
};

test("accepts one exact Node version across local, Docker, and CI inputs", () => {
  assert.deepEqual(findNodeVersionDrift(aligned), []);
});

test("reports every drifted runtime boundary", () => {
  const failures = findNodeVersionDrift({
    ...aligned,
    packageMetadata: { engines: { node: ">=22 <23" } },
    dockerfile: "FROM node:22-alpine@sha256:abc AS build",
    workflows: [["ci.yaml", "uses: actions/setup-node@sha\nnode-version: 22"]],
  });
  assert.equal(failures.length, 3);
});
