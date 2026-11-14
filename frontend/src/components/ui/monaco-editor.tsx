import Editor, { loader } from "@monaco-editor/react";
import { lazy, type ComponentProps } from "react";

// Same-origin ESM assets are built from the locked package with the patched
// sanitizer. No CDN or prebuilt AMD fallback can reintroduce the vendored copy.
const LoadedEditor = lazy(async () => {
  const css = document.createElement("link");
  css.rel = "stylesheet";
  css.href = "/monaco/editor.css";
  const styled = new Promise<void>((resolve, reject) => {
    css.onload = () => resolve();
    css.onerror = () => reject(new Error("Unable to load editor styles"));
  });
  document.head.append(css);
  self.MonacoEnvironment = {
    getWorker(_moduleId, label) {
      const name =
        label === "json"
          ? "json"
          : ["css", "scss", "less"].includes(label)
            ? "css"
            : ["html", "handlebars", "razor"].includes(label)
              ? "html"
              : ["typescript", "javascript"].includes(label)
                ? "ts"
                : "editor";
      const url = `/monaco/${name}.worker.js`;
      return new Worker(url, { type: "module" });
    },
  };
  const url = "/monaco/editor.js";
  const [monaco] = await Promise.all([import(/* @vite-ignore */ url), styled]);
  loader.config({ monaco });
  return { default: Editor };
});

export default function MonacoEditor(props: ComponentProps<typeof Editor>) {
  return <LoadedEditor {...props} />;
}
