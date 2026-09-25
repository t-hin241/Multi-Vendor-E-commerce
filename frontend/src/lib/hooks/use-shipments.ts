"use client";

import { useQuery } from "@tanstack/react-query";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";

export function useMyShipments(enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.myShipments(),
    queryFn: () => callWithAuth((token) => api.listMyShipments(token, { limit: 100 })),
    enabled,
  });
}

export function useShipmentEvents(shipmentId: string, enabled: boolean) {
  const { callWithAuth } = useAuth();
  return useQuery({
    queryKey: queryKeys.shipmentEvents(shipmentId),
    queryFn: () => callWithAuth((token) => api.listShipmentEvents(token, shipmentId)),
    enabled,
  });
}
