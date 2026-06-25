'use client';

// §HostMounts — per-extension error boundary.
//
// The design doc requires that "a broken or hostile extension degrades to a
// placeholder and can never crash the host shell". Every mount rendered by
// ExtensionSlot is wrapped in one of these so a throw inside an extension's
// render (Tier 1 widget or Tier 2 iframe wrapper) is contained to that one
// card — the rest of the dashboard keeps rendering.
//
// React error boundaries must be class components; this is the one class in
// the extensions runtime for exactly that reason.

import { Component, type ReactNode } from 'react';

interface Props {
  // Shown in the fallback so an operator can see which extension failed.
  extensionName: string;
  children: ReactNode;
}

interface State {
  hasError: boolean;
}

export class ExtensionErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false };

  static getDerivedStateFromError(): State {
    return { hasError: true };
  }

  render() {
    if (this.state.hasError) {
      return (
        <div
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-muted-foreground"
        >
          Extension{' '}
          <span className="font-medium text-foreground">{this.props.extensionName}</span>{' '}
          failed to render and was isolated. The rest of the page is unaffected.
        </div>
      );
    }
    return this.props.children;
  }
}
