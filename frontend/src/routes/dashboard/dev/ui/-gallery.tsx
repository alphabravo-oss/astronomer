import { useEffect, useState, type ReactNode } from "react";
import { Download, Plus, Server, Trash2 } from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { CodeBlock } from "@/components/ui/code-block";
import { Combobox } from "@/components/ui/combobox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PermissionState,
} from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Kbd } from "@/components/ui/kbd";
import { MetricCard } from "@/components/ui/metric-card";
import {
  PageHeader,
  PageSection,
  PageShell,
  ResourceMasthead,
} from "@/components/ui/page";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { QueryStates } from "@/components/ui/query-states";
import { Select } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Skeleton, SkeletonCard, SkeletonText } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/status-badge";
import { Switch } from "@/components/ui/switch";
import { TabStrip } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { Tooltip } from "@/components/ui/tooltip";
import { WizardStepper } from "@/components/ui/wizard-stepper";

const INTENTS = ["default", "primary", "destructive", "ghost", "link"] as const;
const SIZES = ["xs", "sm", "md"] as const;

/** Renders the same content in a light and a scoped `.dark` surface. */
function Showcase({
  id,
  title,
  children,
}: {
  id: string;
  title: string;
  children: ReactNode;
}) {
  return (
    <section data-testid={`gallery-${id}`} aria-labelledby={`gallery-${id}-h`}>
      <h2
        id={`gallery-${id}-h`}
        className="mb-2 text-section-title font-semibold text-foreground"
      >
        {title}
      </h2>
      <div className="grid gap-4 xl:grid-cols-2">
        {(["light", "dark"] as const).map((theme) => (
          <div
            key={theme}
            data-gallery-theme={theme}
            className={`${theme === "dark" ? "dark " : ""}min-w-0 space-y-4 rounded-lg border border-border bg-background p-4 text-foreground`}
          >
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              {theme}
            </div>
            {children}
          </div>
        ))}
      </div>
    </section>
  );
}

function Row({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center gap-2">{children}</div>;
}

function ButtonMatrix() {
  return (
    <div className="space-y-3">
      {INTENTS.map((intent) => (
        <div key={intent} className="space-y-1">
          <div className="text-xs text-muted-foreground">{intent}</div>
          <Row>
            {SIZES.map((size) => (
              <ActionButton key={size} intent={intent} size={size}>
                {size}
              </ActionButton>
            ))}
            <ActionButton
              intent={intent}
              icon={<Plus className="h-3.5 w-3.5" />}
            >
              Icon
            </ActionButton>
            <ActionButton intent={intent} disabled>
              Disabled
            </ActionButton>
            <ActionButton
              intent={intent}
              disabled
              disabledReason="Requires the admin role"
            >
              Reason
            </ActionButton>
            <ActionButton intent={intent} loading loadingLabel="Saving">
              Loading
            </ActionButton>
          </Row>
        </div>
      ))}
      <Row>
        <ActionButton
          intent="ghost"
          size="icon"
          aria-label="Download"
          icon={<Download className="h-4 w-4" />}
        />
        <ActionButton
          intent="default"
          size="icon-xs"
          aria-label="Add"
          icon={<Plus className="h-3.5 w-3.5" />}
        />
        <ActionButton intent="primary" tooltip="Styled tooltip text">
          With tooltip
        </ActionButton>
      </Row>
    </div>
  );
}

const STATUSES = [
  "active",
  "healthy",
  "pending",
  "degraded",
  "failed",
  "unknown",
];

function BadgesDemo() {
  return (
    <>
      <Row>
        {(
          [
            "default",
            "secondary",
            "outline",
            "success",
            "warning",
            "error",
            "info",
            "high",
          ] as const
        ).map((variant) => (
          <Badge key={variant} variant={variant}>
            {variant}
          </Badge>
        ))}
      </Row>
      <Row>
        {STATUSES.map((status) => (
          <StatusBadge key={status} status={status} />
        ))}
      </Row>
      <Row>
        <StatusBadge status="active" size="sm" shape="square" />
        <StatusBadge status="failed" size="lg" pulse />
        <StatusBadge tone="blue" label="Production" />
      </Row>
    </>
  );
}

function FormsDemo({ uid }: { uid: string }) {
  const [region, setRegion] = useState<string | null>("eu-west-1");
  const [checked, setChecked] = useState(true);
  const [on, setOn] = useState(true);
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <Input aria-label="Text input" placeholder="Text input" />
      <Input aria-label="Disabled input" placeholder="Disabled" disabled />
      <Input
        aria-label="Invalid input"
        aria-invalid="true"
        defaultValue="invalid value"
      />
      <Select aria-label="Select" defaultValue="b">
        <option value="a">Option A</option>
        <option value="b">Option B</option>
      </Select>
      <Combobox
        aria-label="Region"
        value={region}
        onValueChange={setRegion}
        options={[
          { value: "eu-west-1", label: "eu-west-1", description: "Ireland" },
          { value: "us-east-1", label: "us-east-1", description: "Virginia" },
          { value: "ap-south-1", label: "ap-south-1", description: "Mumbai" },
        ]}
      />
      <Combobox
        aria-label="Disabled combobox"
        value={null}
        onValueChange={() => {}}
        options={[]}
        placeholder="Disabled"
        disabled
      />
      <Textarea
        aria-label="Textarea"
        placeholder="Textarea"
        className="sm:col-span-2"
      />
      <label className="flex items-center gap-2 text-sm" htmlFor={`${uid}-cb`}>
        <Checkbox
          id={`${uid}-cb`}
          checked={checked}
          onChange={(e) => setChecked(e.target.checked)}
        />
        Checkbox
      </label>
      <label className="flex items-center gap-2 text-sm" htmlFor={`${uid}-cb2`}>
        <Checkbox id={`${uid}-cb2`} disabled />
        Disabled checkbox
      </label>
      <Row>
        <Switch aria-label="Switch" checked={on} onCheckedChange={setOn} />
        <Switch aria-label="Small switch" size="sm" checked={!on} />
        <Switch aria-label="Disabled switch" disabled />
      </Row>
    </div>
  );
}

function TabsDemo() {
  const [tab, setTab] = useState("overview");
  return (
    <TabStrip
      aria-label="Gallery tabs"
      value={tab}
      onChange={setTab}
      tabs={[
        { key: "overview", label: "Overview" },
        { key: "nodes", label: "Nodes", count: 12 },
        { key: "events", label: "Events", icon: Server },
      ]}
    />
  );
}

function OverlaysDemo() {
  const [confirm, setConfirm] = useState(false);
  const [typed, setTyped] = useState(false);
  return (
    <>
      <Row>
        <Tooltip content="Tooltip content">
          <ActionButton>Tooltip</ActionButton>
        </Tooltip>
        <Tooltip content="Why this is disabled" wrap>
          <ActionButton disabled>Disabled tooltip</ActionButton>
        </Tooltip>
        <Popover>
          <PopoverTrigger asChild>
            <ActionButton>Popover</ActionButton>
          </PopoverTrigger>
          <PopoverContent>
            <p className="text-sm">Popover content</p>
          </PopoverContent>
        </Popover>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <ActionButton>Menu</ActionButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuLabel>Cluster</DropdownMenuLabel>
            <DropdownMenuItem>Cordon</DropdownMenuItem>
            <DropdownMenuItem>Drain</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem>Remove</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <ActionMenu
          ariaLabel="Row actions"
          items={[
            { label: "Edit", onClick: () => {} },
            { label: "Disabled", onClick: () => {}, disabled: true },
            {
              label: "Delete",
              onClick: () => {},
              variant: "destructive",
              separator: true,
              icon: <Trash2 className="h-4 w-4" />,
            },
          ]}
        />
      </Row>
      <Row>
        <Dialog>
          <DialogTrigger asChild>
            <ActionButton>Dialog</ActionButton>
          </DialogTrigger>
          <DialogContent>
            <DialogTitle className="text-lg font-semibold">Dialog</DialogTitle>
            <DialogDescription className="mt-1 text-sm text-muted-foreground">
              Radix dialog with focus trap and scroll lock.
            </DialogDescription>
            <div className="mt-4 flex justify-end">
              <DialogClose asChild>
                <ActionButton>Close</ActionButton>
              </DialogClose>
            </div>
          </DialogContent>
        </Dialog>
        <Sheet>
          <SheetTrigger asChild>
            <ActionButton>Sheet</ActionButton>
          </SheetTrigger>
          <SheetContent className="p-6">
            <SheetTitle className="text-lg font-semibold">Sheet</SheetTitle>
            <SheetDescription className="mt-1 text-sm text-muted-foreground">
              Edge-anchored panel.
            </SheetDescription>
            <div className="mt-4">
              <SheetClose asChild>
                <ActionButton>Close</ActionButton>
              </SheetClose>
            </div>
          </SheetContent>
        </Sheet>
        <ActionButton intent="destructive" onClick={() => setConfirm(true)}>
          Confirm dialog
        </ActionButton>
        <ActionButton intent="destructive" onClick={() => setTyped(true)}>
          Typed confirm
        </ActionButton>
      </Row>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => setConfirm(false)}
        variant="destructive"
        title="Delete deployment"
        description="This removes the deployment from the cluster."
      />
      <ConfirmDialog
        open={typed}
        onClose={() => setTyped(false)}
        onConfirm={() => setTyped(false)}
        variant="destructive"
        title="Delete cluster"
        description="Type the cluster name to confirm."
        confirmValue="prod-eu-1"
        impact={{
          scope: "Cluster prod-eu-1",
          consequences: ["Agent is removed", "Fleet history is retained"],
          recovery: "Re-register the cluster to restore access.",
        }}
      />
    </>
  );
}

const noop = () => {};

function StatesDemo() {
  return (
    <div className="space-y-4">
      <EmptyState
        icon={Server}
        title="No clusters yet"
        description="Register a cluster to get started."
        actionLabel="Register cluster"
        onAction={noop}
      />
      <EmptyState
        icon={Server}
        title="Nothing to do"
        description="Terminal empty state."
        terminal
      />
      <LoadingState title="Loading clusters" />
      <ErrorState description="The API did not respond." onRetry={noop} />
      <PermissionState permission="clusters:read" />
      <QueryStates
        query={{ isLoading: true, isError: false, refetch: noop }}
        loadingTitle="QueryStates loading"
      >
        <div />
      </QueryStates>
      <QueryStates
        query={{
          isLoading: false,
          isError: true,
          error: new Error("boom"),
          refetch: noop,
        }}
        errorTitle="QueryStates error"
      >
        <div />
      </QueryStates>
      <QueryStates
        query={{ data: [], isLoading: false, isError: false, refetch: noop }}
        isEmpty={(rows: unknown[]) => rows.length === 0}
        empty={<div className="text-sm">QueryStates empty slot</div>}
      >
        <div />
      </QueryStates>
    </div>
  );
}

interface DemoRow {
  name: string;
  status: string;
  version: string;
  nodes: number;
}

const ROWS: DemoRow[] = [
  { name: "prod-eu-1", status: "healthy", version: "v1.31.2", nodes: 12 },
  { name: "prod-us-1", status: "healthy", version: "v1.31.2", nodes: 9 },
  { name: "staging", status: "degraded", version: "v1.30.6", nodes: 4 },
  { name: "dev-sandbox", status: "pending", version: "v1.30.6", nodes: 2 },
  { name: "edge-ams", status: "failed", version: "v1.29.9", nodes: 3 },
];

const COLUMNS: Column<DemoRow>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (r) => r.name,
    sortAccessor: (r) => r.name,
    sortable: true,
  },
  {
    key: "status",
    header: "Status",
    accessor: (r) => <StatusBadge status={r.status} />,
    sortAccessor: (r) => r.status,
    sortable: true,
  },
  {
    key: "version",
    header: "Version",
    accessor: (r) => r.version,
    sortAccessor: (r) => r.version,
  },
  {
    key: "nodes",
    header: "Nodes",
    accessor: (r) => r.nodes,
    sortAccessor: (r) => r.nodes,
    align: "right",
    sortable: true,
  },
];

function TableDemo() {
  return (
    <DataTable
      data={ROWS}
      columns={COLUMNS}
      keyExtractor={(r) => r.name}
      searchable={false}
      pageSize={10}
    />
  );
}

function DensityToggle() {
  const [density, setDensity] = useState<string>(
    () => document.documentElement.dataset.density ?? "comfortable",
  );
  useEffect(() => {
    const root = document.documentElement;
    const original = root.dataset.density;
    if (density === "compact") root.dataset.density = "compact";
    else delete root.dataset.density;
    return () => {
      if (original) root.dataset.density = original;
      else delete root.dataset.density;
    };
  }, [density]);
  return (
    <Row>
      <span className="text-sm text-muted-foreground">Density</span>
      {(["comfortable", "compact"] as const).map((d) => (
        <ActionButton
          key={d}
          size="sm"
          intent={density === d ? "primary" : "default"}
          aria-pressed={density === d}
          onClick={() => setDensity(d)}
        >
          {d}
        </ActionButton>
      ))}
    </Row>
  );
}

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
