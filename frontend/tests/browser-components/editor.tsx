import { Suspense, useState } from "react";
import MonacoEditor from "@/components/ui/monaco-editor";
import { YamlEditor } from "@/components/ui/yaml-editor";
export function EditorFixture() {
  const [value, setValue] = useState("kind: Pod\n");
  const [saved, setSaved] = useState("");
  return (
    <main>
      <section aria-label="YAML editor" style={{ height: 300 }}>
        <YamlEditor
          value={value}
          onChange={setValue}
          onSave={setSaved}
          height="250px"
          className="h-full"
        />
      </section>
      <button onClick={() => setValue("kind: Secret\n")}>
        Load external YAML
      </button>
      <output aria-label="Saved YAML">{saved}</output>
      <Suspense fallback={<p>Loading JSON</p>}>
        <MonacoEditor
          height="200px"
          language="json"
          value={'{"broken": }'}
          onMount={(_editor, monaco) => {
            monaco.editor.onDidChangeMarkers(() => {
              document.getElementById("diagnostics")!.textContent = String(
                monaco.editor.getModelMarkers({}).length,
              );
            });
          }}
        />
      </Suspense>
      <output id="diagnostics" aria-label="JSON diagnostics">
        0
      </output>
    </main>
  );
}
