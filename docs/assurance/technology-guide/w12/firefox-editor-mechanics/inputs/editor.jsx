import React, { useState } from "react";
import { createRoot } from "react-dom/client";
import { YamlEditor } from "../../src/components/ui/yaml-editor";

// This diagnostic uses the actual controlled product editor and selected
// Monaco assets. It contains only synthetic content and has no backend.
function Fixture() {
  const [value, setValue] = useState("apiVersion: v1\nkind: ConfigMap\n");
  const [submitted, setSubmitted] = useState("");
  return (
    <main>
      <h1>Scoped editor keyboard fixture</h1>
      <YamlEditor value={value} onChange={setValue} onDryRun={setSubmitted} height="400px" />
      <pre id="submitted">{submitted}</pre>
    </main>
  );
}
createRoot(document.getElementById("root")).render(<Fixture />);
