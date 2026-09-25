import { Grid2x2 } from "lucide-react";
import Link from "next/link";

import type { Category } from "@/lib/api-client";

// Renders the storefront's root categories as a single-row horizontal
// scroller (not a wrapping grid) so a large category count scrolls sideways
// instead of pushing the rest of the home page down.
export function CategoryTiles({ categories }: { categories: Category[] }) {
  return (
    <div className="flex snap-x snap-mandatory gap-3 overflow-x-auto pb-1">
      {categories.map((c) => (
        <Link
          key={c.id}
          href={`/categories/${c.slug}`}
          className="group flex w-16 shrink-0 snap-start flex-col items-center gap-1.5 rounded-lg p-2 text-center transition-colors hover:bg-accent sm:w-20"
        >
          <span className="flex size-14 items-center justify-center rounded-full border bg-muted text-muted-foreground transition-colors group-hover:border-primary group-hover:text-primary">
            <Grid2x2 className="size-6" />
          </span>
          <span className="line-clamp-2 text-xs text-foreground">{c.name}</span>
        </Link>
      ))}
    </div>
  );
}
