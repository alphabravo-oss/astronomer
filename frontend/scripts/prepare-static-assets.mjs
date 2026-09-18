import { cp, copyFile, mkdir, readFile, rm, writeFile } from "node:fs/promises";

const publicDirectory = new URL("../public/", import.meta.url);
const monacoSource = new URL(
  "../node_modules/monaco-editor/min/vs/",
  import.meta.url,
);
const monacoDestination = new URL("../public/monaco/vs/", import.meta.url);

await mkdir(publicDirectory, { recursive: true });
await copyFile(
  new URL("../node_modules/@wterm/core/wasm/wterm.wasm", import.meta.url),
  new URL("wterm.wasm", publicDirectory),
);

// This directory is generated exclusively from the lockfile-pinned package.
await rm(monacoDestination, { recursive: true, force: true });
await mkdir(new URL("../public/monaco/", import.meta.url), { recursive: true });
await cp(monacoSource, monacoDestination, { recursive: true });

// Monaco upstream embeds its icon font in CSS. Extract it so font-src can stay
// limited to same-origin files instead of admitting every data: font.
const editorCssUrl = new URL("editor/editor.main.css", monacoDestination);
const editorCss = await readFile(editorCssUrl, "utf8");
const embeddedCodicon = editorCss.match(/url\(data:font\/ttf;base64,([^)]+)\)/);
if (!embeddedCodicon) {
  throw new Error("Monaco's embedded codicon font was not found");
}
await writeFile(
  new URL("editor/codicon.ttf", monacoDestination),
  Buffer.from(embeddedCodicon[1], "base64"),
);
await writeFile(
  editorCssUrl,
  editorCss.replace(embeddedCodicon[0], "url(./codicon.ttf)"),
);
