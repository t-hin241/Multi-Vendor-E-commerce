"use client";

import { Store } from "lucide-react";
import { useParams } from "next/navigation";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { PageShell } from "@/components/page-shell";
import { Pagination } from "@/components/pagination";
import { ProductGrid, ProductGridSkeleton } from "@/components/product-grid";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { STOREFRONT_PAGE_SIZE, useStorefrontProducts } from "@/lib/hooks/use-storefront-products";

// Public shop page: no auth, only ever shows an approved shop (the backend
// 404s anything else — see GetPublicProfile in the vendor service). Reuses
// the exact ProductGrid/Pagination the homepage and category pages already
// use, just scoped to this vendor via useStorefrontProducts's vendorId
// option.
export default function ShopPage() {
  const params = useParams<{ id: string }>();
  const [page, setPage] = useState(1);

  const vendorQuery = useQuery({
    queryKey: ["public-vendor", params.id],
    queryFn: () => api.getPublicVendorProfile(params.id),
    enabled: !!params.id,
  });
  const productsQuery = useStorefrontProducts(page, undefined, {
    vendorId: params.id,
    enabled: !!vendorQuery.data,
  });

  function handlePageChange(next: number) {
    setPage(next);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  if (vendorQuery.isPending) {
    return (
      <PageShell maxWidth="lg">
        <LoadingState rows={4} />
      </PageShell>
    );
  }

  if (vendorQuery.error || !vendorQuery.data) {
    return (
      <PageShell maxWidth="lg">
        <EmptyState title="Không tìm thấy cửa hàng" />
      </PageShell>
    );
  }

  const vendor = vendorQuery.data;

  return (
    <PageShell maxWidth="lg">
      {vendor.banner_url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={vendor.banner_url}
          alt=""
          className="h-40 w-full rounded-lg object-cover sm:h-56"
        />
      ) : (
        <div className="h-40 w-full rounded-lg bg-muted sm:h-56" />
      )}

      <div className="mt-4 flex items-center gap-3">
        <div className="flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-full border bg-background">
          {vendor.logo_url ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={vendor.logo_url} alt="" className="size-full object-cover" />
          ) : (
            <Store className="size-6 text-muted-foreground" />
          )}
        </div>
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{vendor.shop_name}</h1>
          {vendor.description && (
            <p className="mt-0.5 text-sm text-muted-foreground">{vendor.description}</p>
          )}
        </div>
      </div>

      {vendor.policy_text && (
        <Card className="mt-4">
          <CardContent className="text-sm whitespace-pre-wrap text-muted-foreground">
            {vendor.policy_text}
          </CardContent>
        </Card>
      )}

      <div className="mt-8">
        <h2 className="text-lg font-medium">Sản phẩm của {vendor.shop_name}</h2>
        <div className="mt-3">
          {productsQuery.isPending && <ProductGridSkeleton />}
          {productsQuery.error && (
            <ErrorState message="Không thể tải sản phẩm." onRetry={productsQuery.refetch} />
          )}
          {productsQuery.data && productsQuery.data.products.length === 0 && (
            <EmptyState title="Cửa hàng chưa có sản phẩm nào" />
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
      </div>
    </PageShell>
  );
}
