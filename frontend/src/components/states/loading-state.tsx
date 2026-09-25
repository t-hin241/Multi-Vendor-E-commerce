import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

// Generic loading placeholder: a stack of skeleton rows. Pass `rows` to
// match the shape of what's loading (e.g. 1 for a detail page, 8 for a grid
// row count), or override with `className` for a fully custom skeleton.
export function LoadingState({ rows = 3, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-3", className)} role="status" aria-label="Loading">
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-16 w-full" />
      ))}
    </div>
  );
}
