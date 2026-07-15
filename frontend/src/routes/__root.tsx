// Stub root route (P1.1). P1.7 fleshes this out to mount <Providers> and the
// not-found boundary.
import { createRootRoute, Outlet } from '@tanstack/react-router';

export const Route = createRootRoute({
  component: () => <Outlet />,
});
