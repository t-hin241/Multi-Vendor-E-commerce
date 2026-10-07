"use client";

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";
import { isCaseActive } from "@/lib/support-cases";

const PAGE_SIZE = 20;
// Matches poll_interval_seconds of the capability: an open case is looked
// at again every 15s while its page is visible; no realtime channel.
const POLL_MS = 15_000;

export function useSupportCapability(enabled: boolean, scope: api.SupportScope = "buyer") {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: [...queryKeys.supportCapability(), scope],
    queryFn: () => callWithAuth((token) => api.getSupportCapability(token, scope)),
    enabled,
    staleTime: 60_000,
  });
}

export type SupportListFilters = {
  status?: string;
  vendorId?: string;
  assignee?: string;
  unassigned?: boolean;
  overdue?: boolean;
};

export function useSupportCases(
  scope: api.SupportScope,
  filters: SupportListFilters,
  enabled: boolean,
) {
  const { callWithAuth } = useAuth();
  return useInfiniteQuery({
    queryKey: queryKeys.supportCases(scope, filters),
    queryFn: ({ pageParam }) =>
      callWithAuth((token) =>
        api.listSupportCases(token, scope, { ...filters, cursor: pageParam, limit: PAGE_SIZE }),
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
    enabled,
    refetchInterval: POLL_MS,
  });
}

export function useSupportCase(scope: api.SupportScope, caseId: string, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.supportCase(scope, caseId),
    queryFn: () => callWithAuth((token) => api.getSupportCase(token, scope, caseId)),
    enabled,
    refetchInterval: (query) =>
      query.state.data && isCaseActive(query.state.data) ? POLL_MS : false,
  });
}

// useSupportCaseRefresh reloads one case and every list after a change.
export function useSupportCaseRefresh(scope: api.SupportScope, caseId: string) {
  const queryClient = useQueryClient();
  return async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.supportCase(scope, caseId) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.supportCasesAll() }),
    ]);
  };
}

export function useCreateSupportCase(orderId: string) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: {
      input: Parameters<typeof api.createSupportCase>[2];
      idempotencyKey: string;
    }) =>
      callWithAuth((token) =>
        api.createSupportCase(token, orderId, vars.input, vars.idempotencyKey),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.supportCasesAll() }),
  });
}

export function useUploadSupportAttachment(scope: api.SupportScope) {
  const { callWithAuth } = useAuth();
  return useMutation({
    mutationFn: (file: File) =>
      callWithAuth((token) => api.uploadSupportAttachment(token, scope, file)),
  });
}
