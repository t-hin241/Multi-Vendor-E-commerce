"use client";

import { useQuery } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";
import { resolveActiveVendor } from "@/lib/vendor";

// ConsoleShop is an accessible shop in the shape the console pages use.
export type ConsoleShop = api.AccessibleShop & { id: string };

// useConsoleShops lists the shops the signed-in person may open (owner or
// staff, AF-17) and resolves the active one. Every console page shares the
// query, so the switcher and the page agree on the shop.
export function useConsoleShops() {
  const { user, callWithAuth, selectedVendorId } = useAuth();
  const enabled = Boolean(user && (user.role === "vendor" || user.role === "buyer"));
  const query = useQuery({
    queryKey: queryKeys.accessibleShops(),
    queryFn: () => callWithAuth((token) => api.listAccessibleShops(token)),
    enabled,
    staleTime: 30_000,
  });
  const shops: ConsoleShop[] = (query.data?.shops ?? []).map((s) => ({ ...s, id: s.vendor_id }));
  return {
    query,
    shops,
    staffEnabled: query.data?.staff_enabled ?? false,
    activeShop: resolveActiveVendor(shops, selectedVendorId),
  };
}
