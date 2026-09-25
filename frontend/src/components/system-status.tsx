"use client";

import { useQuery } from "@tanstack/react-query";
import { CircleAlert, CircleCheck, Loader2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { fetchGatewayHealth } from "@/lib/api-client";
import { queryKeys } from "@/lib/query-keys";

// Dev-diagnostic only, de-emphasized — a small badge, not customer-facing
// copy, so it never competes with the actual storefront content.
export function SystemStatus() {
  const { data, error, isPending } = useQuery({
    queryKey: queryKeys.gatewayHealth(),
    queryFn: fetchGatewayHealth,
  });

  if (isPending) {
    return (
      <Badge variant="outline" className="gap-1 text-muted-foreground">
        <Loader2 className="size-3 animate-spin" />
        Checking API…
      </Badge>
    );
  }

  if (error) {
    return (
      <Badge variant="destructive" className="gap-1">
        <CircleAlert className="size-3" />
        API unreachable — start the backend stack (docker compose up)
      </Badge>
    );
  }

  return (
    <Badge variant="success" className="gap-1">
      <CircleCheck className="size-3" />
      API status: {data?.status}
    </Badge>
  );
}
