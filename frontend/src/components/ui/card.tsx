import type { HTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const cardVariants = cva("border border-border bg-card text-card-foreground", {
  variants: {
    padding: {
      none: "p-0",
      sm: "p-4",
      md: "p-5",
      lg: "p-6",
    },
    radius: {
      md: "rounded-md",
      lg: "rounded-lg",
      xl: "rounded-xl",
    },
  },
  defaultVariants: {
    padding: "none",
    radius: "lg",
  },
});

export interface CardProps
  extends HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof cardVariants> {}

/**
 * `padding` defaults to `"none"` because `CardHeader`/`CardContent`/
 * `CardFooter` already carry their own `p-5` padding — pass a non-`"none"`
 * padding only for a bare `Card` with no sub-component children.
 */
export function Card({ className, padding, radius, ...props }: CardProps) {
  return (
    <div
      className={cn(cardVariants({ padding, radius }), className)}
      {...props}
    />
  );
}

export function CardHeader({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={cn("flex flex-col gap-1.5 p-5", className)} {...props} />
  );
}

export function CardTitle({
  className,
  children,
  ...props
}: HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h3
      className={cn("text-sm font-semibold text-foreground", className)}
      {...props}
    >
      {children}
    </h3>
  );
}

export function CardDescription({
  className,
  ...props
}: HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p className={cn("text-sm text-muted-foreground", className)} {...props} />
  );
}

export function CardContent({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("p-5 pt-0", className)} {...props} />;
}

export function CardFooter({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("flex items-center gap-2 p-5 pt-0", className)}
      {...props}
    />
  );
}
