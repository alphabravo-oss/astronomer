import type {
  ButtonHTMLAttributes,
  ElementType,
  HTMLAttributes,
  KeyboardEvent,
  ReactNode,
} from "react";
import { cn } from "@/lib/utils";

export function Tabs({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("space-y-6", className)} {...props} />;
}

export function TabsList({
  className,
  "aria-label": ariaLabel,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role="tablist"
      aria-label={ariaLabel ?? "Sections"}
      className={cn("flex gap-6 border-b border-border", className)}
      {...props}
    />
  );
}

export function TabsTrigger({
  active,
  className,
  tabIndex,
  "aria-controls": ariaControls,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { active?: boolean }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      aria-controls={ariaControls}
      tabIndex={tabIndex ?? (active ? 0 : -1)}
      className={cn(
        "flex items-center gap-2 border-b-2 pb-3 text-sm font-medium transition-colors focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
        active
          ? "border-foreground text-foreground"
          : "border-transparent text-muted-foreground hover:text-foreground",
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({
  active = true,
  value,
  className,
  children,
  ...props
}: HTMLAttributes<HTMLDivElement> & { active?: boolean; value?: string }) {
  if (!active) return null;
  return (
    <div
      id={value ? `tabpanel-${value}` : undefined}
      role="tabpanel"
      aria-labelledby={value ? `tab-${value}` : undefined}
      tabIndex={0}
      className={cn(
        "animate-fade-in focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
}

/**
 * Moves selection and focus with ArrowLeft/ArrowRight/Home/End (WAI-ARIA
 * "automatic activation" tab pattern), ported from
 * components/resources/resource-detail.tsx's handleTabKeyDown.
 */
function handleTabStripKeyDown<T extends string>(
  event: KeyboardEvent<HTMLButtonElement>,
  index: number,
  tabs: { key: T }[],
  onChange: (key: T) => void,
) {
  let nextIndex: number | undefined;
  switch (event.key) {
    case "ArrowRight":
      nextIndex = (index + 1) % tabs.length;
      break;
    case "ArrowLeft":
      nextIndex = (index - 1 + tabs.length) % tabs.length;
      break;
    case "Home":
      nextIndex = 0;
      break;
    case "End":
      nextIndex = tabs.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  onChange(tabs[nextIndex].key);
  const buttons =
    event.currentTarget.parentElement?.querySelectorAll<HTMLElement>(
      '[role="tab"]',
    );
  buttons?.[nextIndex]?.focus();
}

export function TabStrip<T extends string>({
  tabs,
  value,
  onChange,
  className,
  "aria-label": ariaLabel,
}: {
  tabs: {
    key: T;
    label: ReactNode;
    icon?: ElementType;
    count?: number;
  }[];
  value: T;
  onChange: (key: T) => void;
  className?: string;
  "aria-label"?: string;
}) {
  return (
    <TabsList className={className} aria-label={ariaLabel}>
      {tabs.map((tab, index) => {
        const Icon = tab.icon;
        return (
          <TabsTrigger
            key={tab.key}
            id={`tab-${tab.key}`}
            active={value === tab.key}
            aria-controls={`tabpanel-${tab.key}`}
            onClick={() => onChange(tab.key)}
            onKeyDown={(event) =>
              handleTabStripKeyDown(event, index, tabs, onChange)
            }
          >
            {Icon ? <Icon className="h-4 w-4" /> : null}
            {tab.label}
            {tab.count !== undefined ? (
              <span className="text-muted-foreground">{tab.count}</span>
            ) : null}
          </TabsTrigger>
        );
      })}
    </TabsList>
  );
}
