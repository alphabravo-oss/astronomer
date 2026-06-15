import { toast } from 'sonner';
import { extractApiErrorMessage } from '@/lib/api/errors';

export function formatToastError(prefix: string, err: unknown, fallback = 'Unexpected error'): string {
  const message = extractApiErrorMessage(err) ?? fallback;
  return prefix ? `${prefix}: ${message}` : message;
}

export function toastApiError(prefix: string, err: unknown, fallback?: string): void {
  toast.error(formatToastError(prefix, err, fallback));
}

export function toastError(message: string): void {
  toast.error(message);
}

export function toastInfo(message: string): void {
  toast.info(message);
}

export function toastSuccess(message: string): void {
  toast.success(message);
}

export function toastWarning(message: string): void {
  toast.warning(message);
}
