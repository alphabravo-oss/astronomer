import { fileURLToPath } from "node:url";
export function lockedSanitizerPlugin() {
  const patched = fileURLToPath(
    new URL("../node_modules/dompurify/dist/purify.es.mjs", import.meta.url),
  );
  let replacementUsed = false;
  return {
    name: "monaco-locked-sanitizer",
    enforce: "pre",
    resolveId(source, importer) {
      if (
        source === "./dompurify/dompurify.js" &&
        importer?.endsWith("/vs/base/browser/domSanitize.js")
      ) {
        replacementUsed = true;
        return patched;
      }
    },
    generateBundle() {
      const modules = [...this.getModuleIds()];
      if (
        !replacementUsed ||
        !modules.includes(patched) ||
        modules.some((id) =>
          id.endsWith("/vs/base/browser/dompurify/dompurify.js"),
        )
      ) {
        throw new Error(
          "Monaco must use the locked DOMPurify package, never its vendored copy",
        );
      }
    },
  };
}
