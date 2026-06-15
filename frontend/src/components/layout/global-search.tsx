'use client';

import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Search } from 'lucide-react';
import { cn } from '@/lib/utils';

/**
 * GlobalSearch is the topbar input that opens the cross-cluster search
 * page. It is intentionally light-weight: typing alone does NOT issue a
 * search request — the user must press Enter (or "/" to focus) to
 * navigate to /dashboard/search?name=<query>. This keeps every keystroke
 * cheap and avoids competing with the search page's own debounce.
 *
 * Keyboard shortcut: "/" focuses the input. Cmd/Ctrl+K is reserved for
 * the global command palette.
 */
export function GlobalSearch() {
  const router = useRouter();
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState('');
  // "/" focuses the search input unless the user is already typing in a
  // form control.
  useEffect(() => {
    function onKeydown(e: KeyboardEvent) {
      if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return;
      const active = document.activeElement;
      const isTypingTarget =
        active instanceof HTMLInputElement ||
        active instanceof HTMLTextAreaElement ||
        active instanceof HTMLSelectElement ||
        active?.getAttribute('contenteditable') === 'true';
      if (!isTypingTarget) {
        e.preventDefault();
        inputRef.current?.focus();
        inputRef.current?.select();
      }
    }
    document.addEventListener('keydown', onKeydown);
    return () => document.removeEventListener('keydown', onKeydown);
  }, []);

  const submit = () => {
    const q = value.trim();
    const target = q
      ? `/dashboard/search?name=${encodeURIComponent(q)}`
      : '/dashboard/search';
    router.push(target);
  };

  return (
    <div className="relative w-full max-w-xs">
      <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
      <input
        ref={inputRef}
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            submit();
          }
          if (e.key === 'Escape') {
            inputRef.current?.blur();
          }
        }}
        placeholder="Search resources..."
        aria-label="Global resource search"
        className={cn(
          'w-full h-8 pl-8 pr-12 rounded-md border border-border bg-background text-sm',
          'text-foreground placeholder:text-muted-foreground',
          'focus:outline-none focus:ring-1 focus:ring-ring focus:border-ring',
          'transition-colors'
        )}
      />
      <kbd
        className="absolute right-2 top-1/2 -translate-y-1/2 hidden md:inline-flex items-center gap-0.5
          px-1.5 py-0.5 rounded border border-border text-[10px] text-muted-foreground font-mono pointer-events-none"
      >
        /
      </kbd>
    </div>
  );
}
