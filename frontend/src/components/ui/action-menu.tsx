'use client';

import { useState, useRef, useEffect } from 'react';
import { MoreHorizontal } from 'lucide-react';
import { cn } from '@/lib/utils';

export interface ActionMenuItem {
  label: string;
  icon?: React.ReactNode;
  onClick: () => void;
  variant?: 'default' | 'destructive';
  disabled?: boolean;
  separator?: boolean;
}

interface ActionMenuProps {
  items: ActionMenuItem[];
}

export function ActionMenu({ items }: ActionMenuProps) {
  const [open, setOpen] = useState(false);
  const [flipUp, setFlipUp] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  const handleToggle = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!open && buttonRef.current) {
      const rect = buttonRef.current.getBoundingClientRect();
      const spaceBelow = window.innerHeight - rect.bottom;
      setFlipUp(spaceBelow < 200);
    }
    setOpen(!open);
  };

  return (
    <div ref={ref} className="relative">
      <button
        ref={buttonRef}
        onClick={handleToggle}
        className="inline-flex items-center justify-center h-7 w-7 rounded
          text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
      >
        <MoreHorizontal className="h-4 w-4" />
      </button>

      {open && (
        <div
          className={cn(
            "absolute right-0 w-44 rounded-md border border-border bg-popover p-1 shadow-lg z-50",
            flipUp ? "bottom-full mb-1" : "top-full mt-1"
          )}
          onClick={(e) => e.stopPropagation()}
        >
          {items.map((item, i) => (
            <div key={i}>
              {item.separator && i > 0 && (
                <div className="my-1 h-px bg-border" />
              )}
              <button
                onClick={() => {
                  if (!item.disabled) {
                    item.onClick();
                    setOpen(false);
                  }
                }}
                disabled={item.disabled}
                className={cn(
                  'w-full flex items-center gap-2 px-2.5 py-1.5 rounded text-xs transition-colors',
                  item.disabled && 'opacity-50 cursor-not-allowed',
                  item.variant === 'destructive'
                    ? 'text-status-error hover:bg-status-error/10'
                    : 'text-popover-foreground hover:bg-accent',
                )}
              >
                {item.icon && <span className="flex-shrink-0">{item.icon}</span>}
                {item.label}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
