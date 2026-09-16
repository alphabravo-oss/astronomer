#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as prettier from "prettier";

const frontend = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const root = path.resolve(frontend, "..");

// Check the exact index blob, not the working copy: partially staged changes
// must not pass because a later, unstaged edit happens to be formatted.
const files = execFileSync(
  "git",
  ["diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"],
  {
    cwd: root,
    encoding: "utf8",
  },
)
  .split("\0")
  .filter((file) => file.startsWith("frontend/"));
let checked = 0;
for (const file of files) {
  const filepath = path.join(root, file);
  const info = await prettier.getFileInfo(filepath, {
    ignorePath: path.join(frontend, ".prettierignore"),
    resolveConfig: false,
  });
  if (info.ignored || !info.inferredParser) continue;
  const source = execFileSync("git", ["show", `:${file}`], {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 20 * 1024 * 1024,
  });
  const config = await prettier.resolveConfig(filepath);
  checked += 1;
  if (!(await prettier.check(source, { ...config, filepath }))) {
    console.error(
      `${file}: staged content needs formatting; run prettier --write on the file and stage it again`,
    );
    process.exitCode = 1;
  }
}
console.log(
  `Checked formatting of ${checked} staged frontend files (generated artifacts excluded).`,
);
