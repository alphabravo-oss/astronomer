import { ActionButton } from "@/components/ui/action-button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { InfoCallout } from "@/components/ui/info-callout";
import { Kbd } from "@/components/ui/kbd";
import { MetricCard } from "@/components/ui/metric-card";
import {
  PageHeader,
  PageSection,
  PageShell,
  ResourceMasthead,
} from "@/components/ui/page";
import { Separator } from "@/components/ui/separator";
import { Skeleton, SkeletonCard, SkeletonText } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { WizardStepper } from "@/components/ui/wizard-stepper";
import { Row, Showcase } from "./-gallery-shared";
import {
  BadgesDemo,
  ButtonMatrix,
  FormsDemo,
  TabsDemo,
} from "./-gallery-controls";
import { OverlaysDemo, StatesDemo } from "./-gallery-overlays";
import { DensityToggle, TableDemo } from "./-gallery-table";

export default function UiGallery() {
  return (
    <PageShell>
      <PageHeader
        eyebrow="Development"
        title="UI gallery"
        description="Every components/ui primitive in light and dark. Density follows the interface preference; toggle it here to compare."
        actions={<DensityToggle />}
      />

      <Showcase id="buttons" title="ActionButton">
        <ButtonMatrix />
      </Showcase>
      <Showcase id="badges" title="Badge and StatusBadge">
        <BadgesDemo />
      </Showcase>
      <Showcase id="cards" title="Card and MetricCard">
        <Card>
          <CardHeader>
            <CardTitle>Card title</CardTitle>
          </CardHeader>
          <CardContent>Card content with body text.</CardContent>
        </Card>
        <div className="grid gap-3 sm:grid-cols-2">
          <MetricCard
            label="CPU"
            value="62"
            unit="%"
            percentage={62}
            subtitle="12 of 24 cores"
            trend="up"
            trendValue="+4%"
            sparkline={[3, 5, 4, 7, 6, 9, 8]}
          />
          <MetricCard label="Nodes" value="12" tone="success" dense />
          <MetricCard label="Alerts" value="3" tone="error" />
          <MetricCard label="Loading" value="" loading />
        </div>
      </Showcase>
      <Showcase id="forms" title="Form controls">
        <FormsDemo uid="g" />
      </Showcase>
      <Showcase id="tabs" title="Tabs">
        <TabsDemo />
      </Showcase>
      <Showcase id="overlays" title="Tooltip, Popover, menus, dialogs">
        <OverlaysDemo />
      </Showcase>
      <Showcase id="skeletons" title="Skeleton">
        <Skeleton className="h-4 w-48" />
        <SkeletonText lines={3} />
        <SkeletonCard />
      </Showcase>
      <Showcase id="states" title="EmptyState and QueryStates">
        <StatesDemo />
      </Showcase>
      <Showcase id="page" title="PageHeader, ResourceMasthead, WizardStepper">
        <PageHeader
          title="Clusters"
          description="Registered clusters."
          actions={<ActionButton intent="primary">Register</ActionButton>}
        />
        <ResourceMasthead
          eyebrow="Deployment"
          title="smoke-app"
          mono
          status={<StatusBadge status="healthy" />}
          meta={[
            { label: "Namespace", value: "default" },
            { label: "Replicas", value: "3/3" },
          ]}
          actions={<ActionButton>Restart</ActionButton>}
        />
        <ResourceMasthead title="Loading" loading />
        <PageSection title="PageSection">Section body</PageSection>
        <InfoCallout>Installed releases are cluster-wide.</InfoCallout>
        <InfoCallout
          tone="warning"
          action={<ActionButton size="sm">Review</ActionButton>}
        >
          Two clusters are behind on agent version.
        </InfoCallout>
        <WizardStepper
          currentStep={2}
          steps={[
            { id: "a", label: "Details" },
            { id: "b", label: "Install" },
            { id: "c", label: "Adopt" },
          ]}
        />
      </Showcase>
      <Showcase id="code" title="CodeBlock">
        <CodeBlock
          title="install"
          code={
            "kubectl apply -f agent.yaml\nkubectl rollout status deploy/agent"
          }
          showLineNumbers
        />
      </Showcase>
      <Showcase id="table" title="DataTable">
        <TableDemo />
        {/*
          TODO(031-p4/p8): column kinds, saved views and column pinning are
          landing in another change. Add their demos here once merged.
        */}
        <div
          data-testid="gallery-table-todo"
          className="rounded-md border border-dashed border-border p-3 text-sm text-muted-foreground"
        >
          TODO: column kinds, saved views, column pinning.
        </div>
      </Showcase>
      <Showcase id="misc" title="Kbd and Separator">
        <Row>
          <Kbd>Ctrl</Kbd>
          <Kbd>K</Kbd>
          <span className="text-sm">Open command palette</span>
        </Row>
        <Separator />
        <div className="flex h-8 items-center gap-3 text-sm">
          Left
          <Separator orientation="vertical" />
          Right
        </div>
      </Showcase>
    </PageShell>
  );
}
