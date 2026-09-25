"use client";

import { useQuery } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { queryKeys } from "@/lib/query-keys";

export const STOREFRONT_PAGE_SIZE = 20;

// Fetches one page of the storefront product list. `page` is 1-indexed to
// match the page-number pagination UI directly. `q` is an optional
// free-text search term (already supported end-to-end by the backend).
export function useStorefrontProducts(
  page: number,
  categoryId?: string,
  options: { enabled?: boolean; q?: string; vendorId?: string } = {},
) {
  const { enabled, q, vendorId } = options;
  return useQuery({
    queryKey: queryKeys.storefrontProducts(categoryId ?? null, page, q, vendorId),
    queryFn: () =>
      api.listStorefrontProducts({
        categoryId,
        vendorId,
        q,
        limit: STOREFRONT_PAGE_SIZE,
        offset: (page - 1) * STOREFRONT_PAGE_SIZE,
      }),
    enabled,
  });
}
