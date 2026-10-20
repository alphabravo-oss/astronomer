import { useState } from "react";
import { createRoot } from "react-dom/client";
import { InstallationDestinationView } from "@/components/catalog/installation-destination";
import { OfferingSupport } from "@/components/catalog/offering-support";
import { offeringSupport } from "@/lib/catalogs/assurance";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import "./styles.css";

function OfferingFixture() {
  const [manager, setManager] = useState<
    "Applications" | "Tools" | null
  >(null);
  const [namespace, setNamespace] = useState("observability");
  return (
    <main className="p-6 space-y-4">
      <h1>Platform Tools and Applications</h1>
      <p>
        Component fixture with synthetic destinations. No cluster installation
        is performed.
      </p>
      <div className="flex flex-wrap gap-4">
        <ActionButton onClick={() => setManager("Tools")}>
          Preview Platform Tools
        </ActionButton>
        <ActionButton onClick={() => setManager("Applications")}>
          Preview Applications
        </ActionButton>
      </div>
      <OfferingSupport support={offeringSupport({ tier: "upstream" })} detail />
      <OfferingSupport detail />
      {manager && (
        <ModalShell
          title={`Install through ${manager}`}
          onClose={() => setManager(null)}
          size="lg"
        >
          <InstallationDestinationView
            clusterId="cluster-demo-0123456789"
            clusterName="Development"
            projectId={
              manager === "Applications" ? "project-demo-0123456789" : undefined
            }
            projectName="Observability"
            manager={manager}
            releases={
              manager === "Tools"
                ? [
                    {
                      namespace,
                      releaseName: "metrics-server",
                      version: "1.0.0",
                    },
                    {
                      namespace: "logging",
                      releaseName: "log-collector",
                      version: "2.0.0",
                    },
                  ]
                : [{ namespace, releaseName: "example-app", version: "1.0.0" }]
            }
          />
          <OfferingSupport detail />
          <label className="block">
            Namespace
            <input
              className="block border p-2 w-full"
              value={namespace}
              onChange={(event) => setNamespace(event.target.value)}
            />
          </label>
          <p>
            No assurance is claimed by this fixture. Qualification requires
            retained lifecycle evidence.
          </p>
        </ModalShell>
      )}
    </main>
  );
}
createRoot(document.getElementById("root")!).render(<OfferingFixture />);
