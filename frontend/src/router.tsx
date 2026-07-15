import { createRouter } from '@tanstack/react-router';
import { routeTree } from './routeTree.gen';

// scrollRestoration: true is deliberate (D25): it resets scroll to top on new
// history entries — matching Next App Router's push-navigation behavior — and
// restores position on back/forward. `false` would preserve the current offset
// across pushes, landing detail pages mid-scroll after long-list navigation.
// The useTabParam replace path keeps `resetScroll: false` (P2.1) so tab
// switches still don't jump.
export const router = createRouter({
  routeTree,
  defaultPreload: false,
  scrollRestoration: true,
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
