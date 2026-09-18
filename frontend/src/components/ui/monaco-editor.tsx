import Editor, { loader } from "@monaco-editor/react";

// @monaco-editor/react otherwise defaults to jsDelivr. The matching Monaco
// version from package-lock is copied into the production image at build time.
loader.config({ paths: { vs: "/monaco/vs" } });

export default Editor;
