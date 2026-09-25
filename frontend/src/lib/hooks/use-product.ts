"use client";

import { useQuery } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";

// Passes the current access token (if any) so an admin or the product's own
// vendor viewing this page gets exact stock_quantity back — a plain buyer
// or an anonymous visitor gets the same response as before. A stale/expired
// token just falls back to the anonymous view (see OptionalAuth backend
// side), not worth a refresh-and-retry for this read. user.id (not the
// token itself) is in the query key so logging in/out on an already-open
// product page refetches instead of serving the other viewer's cached
// response.
export function useProduct(slug: string) {
  const { user, accessToken } = useAuth();
  return useQuery({
    queryKey: queryKeys.product(slug, user?.id ?? null),
    queryFn: () => api.getProductBySlug(slug, accessToken ?? undefined),
    enabled: !!slug,
  });
}
