import { NextResponse } from 'next/server';
import type { NextRequest } from 'next/server';

export async function middleware(request: NextRequest) {
  // Skip bootstrap check for static files, api routes, and the bootstrap page itself
  if (
    request.nextUrl.pathname.startsWith('/_next') ||
    request.nextUrl.pathname.startsWith('/api') ||
    request.nextUrl.pathname === '/bootstrap'
  ) {
    return NextResponse.next();
  }

  // Check if system is bootstrapped
  try {
    const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8000';
    const baseUrl = apiUrl.replace('/api/v1', '');
    const res = await fetch(`${baseUrl}/api/v1/bootstrap/`, {
      cache: 'no-store',
      next: { revalidate: 0 },
    });
    const data = await res.json();
    if (!data.bootstrapped) {
      return NextResponse.redirect(new URL('/bootstrap', request.url));
    }
  } catch {
    // If bootstrap check fails, let the request through
  }

  return NextResponse.next();
}

export const config = {
  matcher: ['/((?!_next/static|_next/image|favicon.ico).*)'],
};
