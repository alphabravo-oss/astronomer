#!/usr/bin/env node
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const args = Object.fromEntries(process.argv.slice(2).reduce((rows, value, index, all) => index % 2 === 0 ? [...rows, [value, all[index + 1]]] : rows, []));
const rawDir = path.resolve(args["--raw-dir"] ?? "");
const rawRun = args["--raw-run-id"];
const output = args["--out"];
if (!rawDir || !/^\d+$/.test(rawRun ?? "") || !output) throw new Error("raw directory, run ID, and output are required");
const input = JSON.parse(fs.readFileSync(path.join(rawDir, "human-evaluation.json"), "utf8"));
const expected = ["counterbalanced", "participant_count", "responses", "results_file", "reviewer", "study_version"];
if (JSON.stringify(Object.keys(input).sort()) !== JSON.stringify(expected)) throw new Error("raw human study violates closed schema");
const resultPath = path.resolve(rawDir, input.results_file);
if (!resultPath.startsWith(`${rawDir}${path.sep}`) || !fs.lstatSync(resultPath).isFile() || fs.lstatSync(resultPath).isSymbolicLink()) throw new Error("retained human results file is unsafe or missing");
delete input.results_file;
input.results_uri = `artifact://rancher-human-raw-${rawRun}/${path.relative(rawDir, resultPath).split(path.sep).join("/")}`;
input.results_sha256 = `sha256:${crypto.createHash("sha256").update(fs.readFileSync(resultPath)).digest("hex")}`;
fs.writeFileSync(output, `${JSON.stringify(input)}\n`, {flag: "wx", mode: 0o644});
