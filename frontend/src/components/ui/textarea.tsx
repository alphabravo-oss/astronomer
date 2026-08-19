import { forwardRef, type TextareaHTMLAttributes } from 'react';
import { cn } from '@/lib/utils';
import { controlClassName } from '@/components/ui/input';

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ className, ...props }, ref) => (
    <textarea
      ref={ref}
      className={cn(controlClassName, 'h-auto min-h-[120px] py-2 font-mono text-xs', className)}
      {...props}
    />
  ),
);

Textarea.displayName = 'Textarea';
