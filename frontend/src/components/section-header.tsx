import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

// Shared title/subtitle/action/badge row for page and section headings.
// `as="h1"` at the handful of call sites where this is the page's only
// heading; defaults to `h2` for in-page section headers.
export function SectionHeader({
  title,
  subtitle,
  action,
  badge,
  as = "h2",
  className,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  action?: ReactNode;
  badge?: ReactNode;
  as?: "h1" | "h2";
  className?: string;
}) {
  const Heading = as;
  return (
    <div className={cn("flex flex-wrap items-start justify-between gap-3", className)}>
      <div>
        <div className="flex items-center gap-2">
          <Heading className="text-2xl font-semibold tracking-tight">{title}</Heading>
          {badge}
        </div>
        {subtitle && <p className="mt-1 text-sm text-muted-foreground">{subtitle}</p>}
      </div>
      {action}
    </div>
  );
}
