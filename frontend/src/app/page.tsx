import { redirect } from '@/lib/navigation-server';

export default function RootPage() {
  redirect('/dashboard');
}
