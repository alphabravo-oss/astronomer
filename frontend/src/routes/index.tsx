import { createFileRoute, redirect } from '@tanstack/react-router';

export const Route = createFileRoute('/')({
  beforeLoad: () => {
    // '/dashboard' registers as a route in P2.1; the cast keeps type-check
    // green until then and must be dropped when the route exists.
    throw redirect({ to: '/dashboard' as never });
  },
});
