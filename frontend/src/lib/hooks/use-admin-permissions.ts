"use client";

import { useQuery } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";

// useAdminPermissions reads the signed-in admin's bundles (AF-19).
export function useAdminPermissions() {
  const { user, callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.adminPermissions(user?.id ?? ""),
    queryFn: () => callWithAuth((token) => api.getMyAdminPermissions(token)),
    enabled: user?.role === "admin",
    staleTime: 30_000,
  });
}
