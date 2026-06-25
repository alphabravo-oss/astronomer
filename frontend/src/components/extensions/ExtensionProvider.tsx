'use client';

// §HostMounts — ExtensionProvider: the React context that owns the enabled-
// extension registry for the host runtime. It wraps the dashboard shell once,
// fetches GET /extensions/mounts/ via useEnabledExtensions(), and exposes the
// indexed registry plus the host theme tokens. `useExtensionMounts(point)` is
// the lookup the app uses to find the mounts for a given mount location.
//
// This is the host *runtime* provider only: rendering (DeclarativeWidget /
// SandboxedExtension) lives in sibling components and consumes this context via
// useExtensionMounts. Keeping the provider render-agnostic means a broken or
// hostile extension can never reach the host shell through here.

import { createContext, useContext, useMemo, type ReactNode } from 'react';
import { useEnabledExtensions, emptyRegistry, type ExtensionRegistry } from '@/lib/extensions/registry';
import type { ExtensionMount, ExtensionPointKind } from '@/lib/api/extensions';

// Host theme tokens pushed to Tier-2 iframes on handshake (§Theme). Kept here
// so the provider is the single owner of "what theme do extensions see".
export interface ExtensionTheme {
  mode: 'light' | 'dark';
  tokens: Record<string, string>;
}

export interface ExtensionRuntime {
  // Indexed registry: point -> mounts. Always fully populated (empty buckets).
  registry: ExtensionRegistry;
  // Loading/error mirror the underlying React Query so consumers can gate UI.
  isLoading: boolean;
  isError: boolean;
  // Host theme tokens for Tier-2 handshake/theme pushes; undefined until known.
  theme?: ExtensionTheme;
}

const ExtensionContext = createContext<ExtensionRuntime | null>(null);

export interface ExtensionProviderProps {
  children: ReactNode;
  // Optional host theme; the dashboard shell supplies its resolved tokens. Left
  // optional so the provider works in tests/storybook without a theme source.
  theme?: ExtensionTheme;
}

export function ExtensionProvider({ children, theme }: ExtensionProviderProps) {
  const { data, isLoading, isError } = useEnabledExtensions();

  const value = useMemo<ExtensionRuntime>(
    () => ({
      registry: data ?? emptyRegistry(),
      isLoading,
      isError,
      theme,
    }),
    [data, isLoading, isError, theme],
  );

  return <ExtensionContext.Provider value={value}>{children}</ExtensionContext.Provider>;
}

// Access the whole runtime. Returns a safe empty runtime when used outside a
// provider so a host page that forgets to wrap degrades to "no extensions"
// rather than crashing — the fail-closed posture the design doc asks for.
export function useExtensionRuntime(): ExtensionRuntime {
  const ctx = useContext(ExtensionContext);
  if (ctx) return ctx;
  return { registry: emptyRegistry(), isLoading: false, isError: false };
}

// The lookup the app uses: find the extensions mounted at a given point.
// `<ExtensionSlot point="clusterTab" />` is built on this.
export function useExtensionMounts(point: ExtensionPointKind): ExtensionMount[] {
  const { registry } = useExtensionRuntime();
  return registry[point] ?? [];
}

// Host theme tokens, for the Tier-2 bridge handshake/theme push.
export function useExtensionTheme(): ExtensionTheme | undefined {
  return useExtensionRuntime().theme;
}
