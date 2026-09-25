"use client";

import { Search } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";

import { CategoryTiles } from "@/components/category-tiles";
import { MarketplaceHero } from "@/components/marketplace-hero";
import { PageShell } from "@/components/page-shell";
import { Pagination } from "@/components/pagination";
import { ProductGrid, ProductGridSkeleton } from "@/components/product-grid";
import { SectionHeader } from "@/components/section-header";
import { EmptyState, QueryState } from "@/components/states/query-state";
import { SystemStatus } from "@/components/system-status";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { useCategories } from "@/lib/hooks/use-categories";
import { STOREFRONT_PAGE_SIZE, useStorefrontProducts } from "@/lib/hooks/use-storefront-products";
import { useDebouncedValue } from "@/lib/use-debounced-value";

function StorefrontContent() {
  const searchParams = useSearchParams();
  const qFromUrl = searchParams.get("q") ?? "";
  const [page, setPage] = useState(1);
  const [searchInput, setSearchInput] = useState(qFromUrl);
  const debouncedSearch = useDebouncedValue(searchInput, 400).trim();

  // The nav bar's own search box (present on every page) submits by pushing
  // "/?q=...". Navigating here from another route remounts this component,
  // so the useState initializer above already picks it up — but submitting
  // it while already on "/" is a same-route client-side nav that does NOT
  // remount, so without this the URL's q silently stops driving the page.
  // Adjusted during render (same "sync on a changed derived value" pattern
  // as the page-reset-on-query-change below), not an effect.
  const [lastSyncedQ, setLastSyncedQ] = useState(qFromUrl);
  if (qFromUrl !== lastSyncedQ) {
    setLastSyncedQ(qFromUrl);
    setSearchInput(qFromUrl);
  }

  // Reset to page 1 whenever the (debounced) search term changes — adjusted
  // during render, same pattern as categories/[slug]/page.tsx's slug-driven
  // reset, so it doesn't need an effect.
  const [pageResetForQuery, setPageResetForQuery] = useState(debouncedSearch);
  if (debouncedSearch !== pageResetForQuery) {
    setPageResetForQuery(debouncedSearch);
    setPage(1);
  }

  const categoriesQuery = useCategories();
  const productsQuery = useStorefrontProducts(page, undefined, {
    q: debouncedSearch || undefined,
  });

  const rootCategories = categoriesQuery.data?.filter((c) => c.parent_id == null) ?? [];

  function handlePageChange(next: number) {
    setPage(next);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  return (
    <PageShell maxWidth="lg">
      <MarketplaceHero statusBadge={<SystemStatus />} />

      <div className="relative mt-6 max-w-md">
        <Search className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          placeholder="Tìm sản phẩm…"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          className="pl-9"
          aria-label="Tìm sản phẩm"
        />
      </div>

      {rootCategories.length > 0 && (
        <div className="mt-8">
          <SectionHeader title="Danh mục" />
          <div className="mt-3">
            <CategoryTiles categories={rootCategories} />
          </div>
        </div>
      )}

      <div className="mt-8">
        <SectionHeader
          title={debouncedSearch ? `Kết quả tìm kiếm cho "${debouncedSearch}"` : "Sản phẩm nổi bật"}
        />
        <div className="mt-3">
          <QueryState
            query={productsQuery}
            loading={<ProductGridSkeleton />}
            isEmpty={(data) => data.products.length === 0}
            emptyState={
              <EmptyState
                title={debouncedSearch ? "Không tìm thấy sản phẩm phù hợp" : "Chưa có sản phẩm nào"}
                description={
                  debouncedSearch
                    ? "Thử một từ khóa tìm kiếm khác."
                    : "Quay lại sau — sản phẩm mới sẽ được cập nhật thường xuyên."
                }
              />
            }
          >
            {(data) => (
              <>
                {(data.vendor_info_degraded || data.sales_info_degraded) && (
                  <Alert variant="warning" className="mb-4">
                    <AlertDescription>
                      Một số thông tin (tên shop / lượt bán) có thể chưa được cập nhật, vui lòng thử
                      lại sau.
                    </AlertDescription>
                  </Alert>
                )}
                <ProductGrid products={data.products} />
                <Pagination
                  currentPage={page}
                  totalPages={Math.ceil(data.total / STOREFRONT_PAGE_SIZE)}
                  onPageChange={handlePageChange}
                />
              </>
            )}
          </QueryState>
        </div>
      </div>
    </PageShell>
  );
}

export default function StorefrontPage() {
  return (
    <Suspense
      fallback={
        <PageShell maxWidth="lg">
          <ProductGridSkeleton />
        </PageShell>
      }
    >
      <StorefrontContent />
    </Suspense>
  );
}
