import assert from "node:assert/strict";
import test from "node:test";

import { findNodeVersionDrift } from "./check-node-version-alignment.mjs";

const aligned = {
  version: "26.11.1",
  packageMetadata: { engines: { node: ">=26.11.1 <27" } },
  dockerfile: "FROM node:26.11.1-alpine@sha256:abc AS build",
  workflows: [
    ["ci.yaml", "uses: actions/setup-node@sha\nwith:\n  node-version: 26.11.1"],
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
