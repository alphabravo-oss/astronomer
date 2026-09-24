import type { ReactNode } from "react";
import { Input } from "@/components/ui/input";

type OptionProps = {
  name: string;
  checked: boolean;
  disabled: boolean;
  onChange: (checked: boolean) => void;
  onBlur: () => void;
};

function RegistrationOption({
  title,
  children,
  ...props
}: OptionProps & { title: string; children: ReactNode }) {
  return (
    <label className="flex items-start gap-3 rounded-lg border border-border bg-muted/20 p-4">
      <Input
        name={props.name}
        aria-label={title}
        type="checkbox"
        checked={!props.disabled && props.checked}
        disabled={props.disabled}
        onChange={(event) => props.onChange(event.target.checked)}
        onBlur={props.onBlur}
        className="mt-0.5 h-4 w-4 rounded-sm border-border text-primary focus:ring-ring disabled:cursor-not-allowed"
      />
      <div>
        <span className="text-sm font-medium">{title}</span>
        {children}
      </div>
    </label>
  );
}

export function RegistrationBaselineOption(props: OptionProps) {
  return (
    <RegistrationOption
      {...props}
      title="Quick Start: Install Platform Baseline"
    >
      <p className="mt-1 text-xs text-muted-foreground">
        Installs kube-state-metrics and prometheus-node-exporter through Flux
        after the agent connects. Image scanning is configured separately below.
        Full monitoring, logging, ingress, and certificate management are
        separate add-ons.
      </p>
    </RegistrationOption>
  );
}

export function RegistrationImageScanningOption(props: OptionProps) {
  return (
    <RegistrationOption {...props} title="Enable image vulnerability scanning">
      <p className="mt-1 text-xs text-muted-foreground">
        Trivy is installed automatically and scans workload images for Image
        Scans, even when Quick Start is off. Uncheck to opt out. Scans need
        registry and vulnerability database access and use additional resources.
        This is vulnerability detection, not runtime protection.
      </p>
    </RegistrationOption>
  );
}
