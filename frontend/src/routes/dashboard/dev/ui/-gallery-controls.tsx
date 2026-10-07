import { useState } from "react";
import { Download, Plus, Server } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Combobox } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/status-badge";
import { Switch } from "@/components/ui/switch";
import { TabStrip } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { Row } from "./-gallery-shared";

const INTENTS = ["default", "primary", "destructive", "ghost", "link"] as const;
const SIZES = ["xs", "sm", "md"] as const;

export function ButtonMatrix() {
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

export function BadgesDemo() {
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

export function FormsDemo({ uid }: { uid: string }) {
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

export function TabsDemo() {
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
