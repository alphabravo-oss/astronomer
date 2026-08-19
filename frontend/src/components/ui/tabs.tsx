import type { ButtonHTMLAttributes, ElementType, HTMLAttributes, ReactNode } from 'react';
import { cn } from '@/lib/utils';

export function Tabs({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('space-y-6', className)} {...props} />;
}

export function TabsList({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return <nav className={cn('flex gap-6 border-b border-border', className)} {...props} />;
}

export function TabsTrigger({
  active,
  className,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { active?: boolean }) {
  return (
    <button
      type="button"
      className={cn(
        'flex items-center gap-2 border-b-2 pb-3 text-sm font-medium transition-colors',
        active
          ? 'border-foreground text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground',
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({
  active = true,
  className,
  children,
  ...props
}: HTMLAttributes<HTMLDivElement> & { active?: boolean }) {
  if (!active) return null;
  return (
    <div className={cn('animate-fade-in', className)} {...props}>
      {children}
    </div>
  );
}

export function TabStrip<T extends string>({
  tabs,
  value,
  onChange,
  className,
}: {
  tabs: { key: T; label: ReactNode; icon?: ElementType }[];
  value: T;
  onChange: (key: T) => void;
  className?: string;
}) {
  return (
    <TabsList className={className}>
      {tabs.map((tab) => {
        const Icon = tab.icon;
        return (
          <TabsTrigger key={tab.key} active={value === tab.key} onClick={() => onChange(tab.key)}>
            {Icon ? <Icon className="h-4 w-4" /> : null}
            {tab.label}
          </TabsTrigger>
        );
      })}
    </TabsList>
  );
}
