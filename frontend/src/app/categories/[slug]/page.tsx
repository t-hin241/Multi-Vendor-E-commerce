"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";

import { PageShell } from "@/components/page-shell";
import { Pagination } from "@/components/pagination";
import { ProductGrid, ProductGridSkeleton } from "@/components/product-grid";
import { SectionHeader } from "@/components/section-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { SubcategoryBar } from "@/components/subcategory-bar";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Button } from "@/components/ui/button";
import type { Category } from "@/lib/api-client";
import { useCategories } from "@/lib/hooks/use-categories";
import { STOREFRONT_PAGE_SIZE, useStorefrontProducts } from "@/lib/hooks/use-storefront-products";

export default function CategoryPage() {
  const params = useParams<{ slug: string }>();
  const [page, setPage] = useState(1);

  // Navigating between categories re-renders this same page component rather
  // than remounting it, so pagination has to reset per slug. Adjusting state
  // during render (React's documented pattern for this, not an effect) keeps
  // the reset in the same render pass instead of causing an extra one.
  const [pageResetForSlug, setPageResetForSlug] = useState(params.slug);
  if (params.slug !== pageResetForSlug) {
    setPageResetForSlug(params.slug);
    setPage(1);
  }

  const categoriesQuery = useCategories();
  const categories = categoriesQuery.data ?? [];
  const category = categories.find((c) => c.slug === params.slug);
  const subcategories = categories.filter((c) => c.parent_id === category?.id);

  // Full ancestor chain, root-first, ending with the current category —
  // e.g. "Điện Gia Dụng" -> "Đồ dùng nhà bếp" -> "Nồi điện các loại".
  const breadcrumb: Category[] = [];
  for (let node = category; node; node = categories.find((c) => c.id === node!.parent_id)) {
    breadcrumb.unshift(node);
  }

  const productsQuery = useStorefrontProducts(page, category?.id, { enabled: !!category });

  function handlePageChange(next: number) {
    setPage(next);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  if (categoriesQuery.isPending) {
    return (
      <PageShell maxWidth="lg">
        <LoadingState rows={1} className="mb-6" />
        <ProductGridSkeleton />
      </PageShell>
    );
  }

  if (categoriesQuery.error) {
    return (
      <PageShell maxWidth="lg">
        <ErrorState message="Không thể tải danh mục." onRetry={categoriesQuery.refetch} />
      </PageShell>
    );
  }

  if (!category) {
    return (
      <PageShell maxWidth="lg">
        <EmptyState title="Không tìm thấy danh mục" />
      </PageShell>
    );
  }

  return (
    <PageShell maxWidth="lg">
      <Breadcrumb className="text-xs text-muted-foreground">
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink asChild>
              <Link href="/">Trang chủ</Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          {breadcrumb.map((c, i) => (
            <span key={c.id} className="contents">
              <BreadcrumbSeparator />
              <BreadcrumbItem>
                {i === breadcrumb.length - 1 ? (
                  <BreadcrumbPage>{c.name}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink asChild>
                    <Link href={`/categories/${c.slug}`}>{c.name}</Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
            </span>
          ))}
        </BreadcrumbList>
      </Breadcrumb>

      <SectionHeader
        as="h1"
        className="mt-2"
        title={category.name}
        badge={
          productsQuery.data && (
            <span className="text-sm text-muted-foreground">
              {productsQuery.data.total} sản phẩm
            </span>
          )
        }
      />

      {/* Subcategories, one level down, as a horizontal pill bar — the
          same bar recurs on whatever page each pill leads to, as deep as
          the category tree goes. */}
      <div className="mt-4">
        <SubcategoryBar categories={subcategories} current={category} />
      </div>

      <div className="mt-8">
        {productsQuery.isPending && <ProductGridSkeleton />}
        {productsQuery.error && (
          <ErrorState message="Không thể tải sản phẩm." onRetry={productsQuery.refetch} />
        )}
        {productsQuery.data && productsQuery.data.products.length === 0 && (
          <EmptyState
            title="Không có sản phẩm trong danh mục này"
            description="Hãy thử danh mục khác, hoặc quay lại sau."
            action={
              <Button asChild>
                <Link href="/">Về trang chủ</Link>
              </Button>
            }
          />
        )}
        {productsQuery.data && productsQuery.data.products.length > 0 && (
          <>
            <ProductGrid products={productsQuery.data.products} />
            <Pagination
              currentPage={page}
              totalPages={Math.ceil(productsQuery.data.total / STOREFRONT_PAGE_SIZE)}
              onPageChange={handlePageChange}
            />
          </>
        )}
      </div>
    </PageShell>
  );
}
