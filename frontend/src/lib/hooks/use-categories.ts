"use client";

import { useQuery } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { queryKeys } from "@/lib/query-keys";

export function useCategories() {
  return useQuery({
    queryKey: queryKeys.categories(),
    queryFn: api.listCategories,
  });
}
