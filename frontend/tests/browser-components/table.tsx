import { useState } from "react";
import { createRoot } from "react-dom/client";
import { DataTable } from "@/components/ui/data-table";
import "./styles.css";

const rows = Array.from({ length: 1200 }, (_, index) => ({
  id: String(index).padStart(4, "0"),
  name: `Item ${String(index).padStart(4, "0")}`,
  namespace: ["alpha", "beta", "gamma"][index % 3],
}));
function TableFixture() {
  const [grouped, setGrouped] = useState(true);
  const [opened, setOpened] = useState("");
  return (
    <main className="p-4">
      <h1>Virtualized grouped table</h1>
      <label>
        <input
          type="checkbox"
          checked={grouped}
          onChange={(event) => setGrouped(event.target.checked)}
        />
        Group namespaces
      </label>
      <output aria-label="Opened row">{opened || "None"}</output>
      <DataTable
        data={rows}
        virtualized
        groupBy={grouped ? (row) => row.namespace : undefined}
        columns={[
          { key: "name", header: "Name", accessor: (row) => row.name },
          {
            key: "namespace",
            header: "Namespace",
            accessor: (row) => row.namespace,
          },
          {
            key: "control",
            header: "Control",
            sortable: false,
            accessor: (row) => (
              <input
                aria-label={`Edit ${row.id}`}
                defaultValue={row.id}
                className="w-20"
              />
            ),
          },
        ]}
        keyExtractor={(row) => row.id}
        selectable={(row) => row.id !== "0003"}
        bulkActions={(selected) => (
          <output aria-label="Selected IDs">
            {selected
              .map((row) => row.id)
              .sort()
              .join(",")}
          </output>
        )}
        onRowClick={(row) => setOpened(row.id)}
        searchPlaceholder="Filter items"
        emptyState={{
          title: "No matching items",
          description: "Clear the filter to restore items.",
        }}
      />
    </main>
  );
}
createRoot(document.getElementById("root")!).render(<TableFixture />);
