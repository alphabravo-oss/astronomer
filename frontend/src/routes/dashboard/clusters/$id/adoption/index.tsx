import { createFileRoute } from "@tanstack/react-router";
// Cluster adoption timeline on cluster detail.
//
// Mirrors the registration wizard's progress page but uses product language
// that matches Astronomer's scope: adopting existing clusters and applying
// optional baselines, not provisioning infrastructure.
import { useState } from "react";
import { Activity, CheckCircle2, ClipboardList, History } from "lucide-react";

import { RegistrationTimeline } from "@/components/clusters/registration-timeline";
import { PageHeader, PageShell } from "@/components/ui/page";
import { TabStrip, TabsContent } from "@/components/ui/tabs";

type AdoptionTab = "overview" | "readiness" | "plan" | "history";

function ClusterAdoptionPage() {
  const params = Route.useParams();
  const clusterId = String(params?.id ?? "");
  const [tab, setTab] = useState<AdoptionTab>("overview");

  return (
    <PageShell>
      <PageHeader
        title="Adoption"
        eyebrow="Cluster lifecycle"
        description="Registration, connection readiness, and the optional platform baseline plan for this existing cluster."
      />
      <TabStrip<AdoptionTab>
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "overview", label: "Overview", icon: Activity },
          { key: "readiness", label: "Readiness", icon: CheckCircle2 },
          { key: "plan", label: "Plan", icon: ClipboardList },
          { key: "history", label: "History", icon: History },
        ]}
      />
      <TabsContent active={tab === "overview"} className="space-y-4">
        <div className="grid gap-3 md:grid-cols-3">
          <AdoptionCard
            title="Register"
            text="Create the cluster identity and a short-lived installation manifest."
          />
          <AdoptionCard
            title="Connect"
            text="Confirm the agent tunnel and read-only inventory are healthy."
          />
          <AdoptionCard
            title="Baseline"
            text="Apply only the optional platform components selected during registration."
          />
        </div>
        <RegistrationTimeline clusterId={clusterId} embedded />
      </TabsContent>
      <TabsContent active={tab === "readiness"}>
        <RegistrationTimeline clusterId={clusterId} embedded view="readiness" />
      </TabsContent>
      <TabsContent active={tab === "plan"}>
        <RegistrationTimeline clusterId={clusterId} embedded view="plan" />
      </TabsContent>
      <TabsContent active={tab === "history"}>
        <RegistrationTimeline clusterId={clusterId} embedded />
      </TabsContent>
    </PageShell>
  );
}

function AdoptionCard({ title, text }: { title: string; text: string }) {
  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <h2 className="text-sm font-semibold text-foreground">{title}</h2>
      <p className="mt-1 text-sm text-muted-foreground">{text}</p>
    </section>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/adoption/")({
  component: ClusterAdoptionPage,
});
