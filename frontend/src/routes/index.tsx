// Stub index route (P1.1). P1.7 replaces this with the /dashboard redirect.
import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/')({
  component: () => null,
});
