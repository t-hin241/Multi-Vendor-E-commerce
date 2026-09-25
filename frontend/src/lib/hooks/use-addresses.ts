"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";

export function useAddresses(enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.buyerAddresses(),
    queryFn: () => callWithAuth((token) => api.listBuyerAddresses(token)),
    enabled,
  });
}

// Shared by every address mutation below: invalidate the list and toast the
// outcome, so each hook only has to supply its own mutationFn.
function useAddressMutation<TVars>(
  perform: (
    callWithAuth: ReturnType<typeof useAuth>["callWithAuth"],
    vars: TVars,
  ) => Promise<unknown>,
  successMessage: string,
  errorFallback: string,
) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (vars: TVars) => perform(callWithAuth, vars),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.buyerAddresses() });
      toast.success(successMessage);
    },
    onError: (err) => toast.error(describeApiError(err, errorFallback)),
  });
}

export function useAddAddress() {
  return useAddressMutation<api.AddressInput>(
    (callWithAuth, input) => callWithAuth((token) => api.addBuyerAddress(token, input)),
    "Address added.",
    "Could not add address.",
  );
}

export function useUpdateAddress() {
  return useAddressMutation<{ id: string; input: api.AddressInput }>(
    (callWithAuth, { id, input }) =>
      callWithAuth((token) => api.updateBuyerAddress(token, id, input)),
    "Address updated.",
    "Could not update address.",
  );
}

export function useDeleteAddress() {
  return useAddressMutation<string>(
    (callWithAuth, id) => callWithAuth((token) => api.deleteBuyerAddress(token, id)),
    "Address removed.",
    "Could not remove address.",
  );
}

export function useSetDefaultAddress() {
  return useAddressMutation<string>(
    (callWithAuth, id) => callWithAuth((token) => api.setDefaultBuyerAddress(token, id)),
    "Default address updated.",
    "Could not set default address.",
  );
}
