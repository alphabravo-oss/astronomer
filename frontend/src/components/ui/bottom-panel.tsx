'use client';

import { useState, useRef, useCallback, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { X, GripHorizontal } from 'lucide-react';
import { cn } from '@/lib/utils';

interface BottomPanelTab {
  key: string;
  label: string;
  icon?: React.ReactNode;
}

interface BottomPanelProps {
  open: boolean;
  onClose: () => void;
  title: string;
  subtitle?: string;
  tabs?: BottomPanelTab[];
  activeTab?: string;
  onTabChange?: (key: string) => void;
  children: React.ReactNode;
}

export function BottomPanel({
  open,
  onClose,
  title,
  subtitle,
  tabs,
  activeTab,
  onTabChange,
  children,
}: BottomPanelProps) {
  const [height, setHeight] = useState(40); // vh
  const panelRef = useRef<HTMLDivElement>(null);
  const dragging = useRef(false);
  const startY = useRef(0);
  const startHeight = useRef(40);
  const [portalTarget, setPortalTarget] = useState<HTMLElement | null>(null);

  useEffect(() => {
    setPortalTarget(document.getElementById('bottom-panel-root'));
  }, []);

  const onDragStart = useCallback((e: React.MouseEvent) => {
    dragging.current = true;
    startY.current = e.clientY;
    startHeight.current = height;
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';
  }, [height]);

  useEffect(() => {
    function onMouseMove(e: MouseEvent) {
      if (!dragging.current) return;
      const delta = startY.current - e.clientY;
      const newHeight = startHeight.current + (delta / window.innerHeight) * 100;
      setHeight(Math.max(20, Math.min(80, newHeight)));
    }
    function onMouseUp() {
      dragging.current = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    }
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', onMouseUp);
    return () => {
      document.removeEventListener('mousemove', onMouseMove);
      document.removeEventListener('mouseup', onMouseUp);
    };
  }, []);

  // Close on Escape
  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  if (!portalTarget) return null;

  return createPortal(
    <div
      ref={panelRef}
      className="border-t border-border bg-card flex flex-col shrink-0 overflow-hidden"
      style={{ height: open ? `${height}vh` : '0px' }}
    >
      {open && (
        <>
          {/* Drag handle */}
          <div
            onMouseDown={onDragStart}
            className="flex items-center justify-center h-2 cursor-row-resize hover:bg-accent/50 transition-colors shrink-0"
          >
            <GripHorizontal className="h-3 w-3 text-muted-foreground" />
          </div>

          {/* Header */}
          <div className="flex items-center justify-between px-4 py-2 border-b border-border shrink-0">
            <div className="flex items-center gap-4 min-w-0">
              <div className="min-w-0">
                <span className="text-sm font-medium text-foreground truncate block">{title}</span>
                {subtitle && (
                  <span className="text-xs text-muted-foreground truncate block">{subtitle}</span>
                )}
              </div>

              {/* Tabs */}
              {tabs && tabs.length > 1 && (
                <div className="flex items-center gap-1 border-l border-border pl-4">
                  {tabs.map((tab) => (
                    <button
                      key={tab.key}
                      onClick={() => onTabChange?.(tab.key)}
                      className={cn(
                        'inline-flex items-center gap-1.5 px-2.5 py-1 rounded text-xs transition-colors',
                        activeTab === tab.key
                          ? 'bg-accent text-foreground'
                          : 'text-muted-foreground hover:text-foreground hover:bg-accent/50',
                      )}
                    >
                      {tab.icon}
                      {tab.label}
                    </button>
                  ))}
                </div>
              )}
            </div>

            <button
              onClick={onClose}
              className="inline-flex items-center justify-center h-7 w-7 rounded
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors shrink-0"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Body */}
          <div className="flex-1 min-h-0 overflow-hidden">
            {children}
          </div>
        </>
      )}
    </div>,
    portalTarget
  );
}
