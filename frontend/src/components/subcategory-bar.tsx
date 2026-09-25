import Link from "next/link";

import { badgeVariants } from "@/components/ui/badge";
import type { Category } from "@/lib/api-client";
import { cn } from "@/lib/utils";

// A horizontal, scrollable bar of subcategory pills shown on a category
// page — distinct from the storefront home page's root-category icon grid
// (CategoryTiles). Each pill drills one level deeper, into that
// subcategory's own page (same product-grid + subcategory-bar layout,
// recursively, down to however deep the tree goes). The leading "Tất cả"
// pill always points back at `current` (the category being viewed) — it's
// the only pill that can honestly be marked active, since `categories` here
// is always `current`'s children, never `current` itself.
export function SubcategoryBar({
  categories,
  current,
}: {
  categories: Category[];
  current: Category;
}) {
  return (
    <div className="flex gap-2 overflow-x-auto pb-1">
      <Link
        href={`/categories/${current.slug}`}
        aria-current="page"
        className={cn(badgeVariants({ variant: "default" }), "h-8 shrink-0 px-3 text-sm")}
      >
        Tất cả
      </Link>
      {categories.map((c) => (
        <Link
          key={c.id}
          href={`/categories/${c.slug}`}
          className={cn(badgeVariants({ variant: "outline" }), "h-8 shrink-0 px-3 text-sm")}
        >
          {c.name}
        </Link>
      ))}
    </div>
  );
}
