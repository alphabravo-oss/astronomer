import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';

// The bootstrap-wizard flow that used to live here (server-side check +
// redirect to /bootstrap) was removed when astronomer-go moved to the
// Rancher-style admin-on-first-boot model. The dashboard layout now handles
// the must_change_password redirect client-side from the auth store.
export function middleware(_request: NextRequest) {
  return NextResponse.next();
}

export const config = {
  matcher: ['/((?!_next/static|_next/image|favicon.ico).*)'],
};
