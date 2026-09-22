/**
 * Inline provider badge for cloud-credential rows / cards. Lucide doesn't
 * ship cloud-vendor brand marks so we use a neutral icon + a coloured
 * background; this keeps things accessible without pulling in a brand-asset
 * SDK just for three icons.
 */
import { Cloud, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import type { CloudProvider } from "@/lib/api/project-detail";

const providerMeta: Record<
  CloudProvider,
  { label: string; tint: string; icon: LucideIcon }
> = {
  aws: {
    label: "AWS",
    tint: "bg-brand-aws/10 text-brand-aws",
    icon: Cloud,
  },
  gcp: {
    label: "GCP",
    tint: "bg-brand-gcp/10 text-brand-gcp",
    icon: Cloud,
  },
  azure: {
    label: "Azure",
    tint: "bg-brand-azure/10 text-brand-azure",
    icon: Cloud,
  },
  digitalocean: {
    label: "DigitalOcean",
    tint: "bg-brand-do/10 text-brand-do",
    icon: Cloud,
  },
  generic: {
    label: "Generic",
    tint: "bg-muted text-muted-foreground",
    icon: Cloud,
  },
};

export function ProviderBadge({ provider }: { provider: CloudProvider }) {
  const meta = providerMeta[provider] ?? providerMeta.generic;
  const Icon = meta.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-sm text-xs font-medium",
        meta.tint,
      )}
    >
      <Icon className="h-3 w-3" />
      {meta.label}
    </span>
  );
}
