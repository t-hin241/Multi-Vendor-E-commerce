import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";

// Builds the sequence of page buttons to render: always the first and last
// page, the current page and its immediate neighbors, and a single "…" for
// any gap in between — so a 50-page list doesn't render 50 buttons.
function getPageNumbers(current: number, total: number): (number | "ellipsis")[] {
  const pages: (number | "ellipsis")[] = [1];
  const windowStart = Math.max(2, current - 1);
  const windowEnd = Math.min(total - 1, current + 1);

  if (windowStart > 2) pages.push("ellipsis");
  for (let p = windowStart; p <= windowEnd; p++) pages.push(p);
  if (windowEnd < total - 1) pages.push("ellipsis");
  if (total > 1) pages.push(total);

  return pages;
}

type PaginationProps =
  | { currentPage: number; totalPages: number; onPageChange: (page: number) => void; hasNextPage?: undefined }
  // Used when the backend doesn't report a total (e.g. orders list) — renders
  // just the Prev/Next pair, no page-number buttons.
  | { currentPage: number; hasNextPage: boolean; onPageChange: (page: number) => void; totalPages?: undefined };

export function Pagination(props: PaginationProps) {
  const { currentPage, onPageChange } = props;

  if (props.totalPages !== undefined) {
    const { totalPages } = props;
    if (totalPages <= 1) return null;

    return (
      <nav aria-label="Pagination" className="mt-8 flex items-center justify-center gap-1">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => onPageChange(currentPage - 1)}
          disabled={currentPage === 1}
          aria-label="Previous page"
        >
          <ChevronLeft />
        </Button>

        {getPageNumbers(currentPage, totalPages).map((p, i) =>
          p === "ellipsis" ? (
            <span key={`ellipsis-${i}`} className="px-2 text-sm text-muted-foreground">
              …
            </span>
          ) : (
            <Button
              key={p}
              variant={p === currentPage ? "default" : "ghost"}
              size="icon"
              onClick={() => onPageChange(p)}
              aria-current={p === currentPage ? "page" : undefined}
            >
              {p}
            </Button>
          ),
        )}

        <Button
          variant="ghost"
          size="icon"
          onClick={() => onPageChange(currentPage + 1)}
          disabled={currentPage === totalPages}
          aria-label="Next page"
        >
          <ChevronRight />
        </Button>
      </nav>
    );
  }

  const { hasNextPage } = props;
  if (!hasNextPage && currentPage === 1) return null;

  return (
    <nav aria-label="Pagination" className="mt-8 flex items-center justify-center gap-1">
      <Button
        variant="ghost"
        size="icon"
        onClick={() => onPageChange(currentPage - 1)}
        disabled={currentPage === 1}
        aria-label="Previous page"
      >
        <ChevronLeft />
      </Button>
      <span className="px-2 text-sm text-muted-foreground">Page {currentPage}</span>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => onPageChange(currentPage + 1)}
        disabled={!hasNextPage}
        aria-label="Next page"
      >
        <ChevronRight />
      </Button>
    </nav>
  );
}
