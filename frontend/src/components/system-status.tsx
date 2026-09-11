"use client";

import { useQuery } from "@tanstack/react-query";

import { fetchGatewayHealth } from "@/lib/api-client";

export function SystemStatus() {
  const { data, error, isPending } = useQuery({
    queryKey: ["gateway-health"],
    queryFn: fetchGatewayHealth,
  });

  if (isPending) {
    return <p className="text-sm text-slate-500">Checking API Gateway…</p>;
  }

  if (error) {
    return (
      <p className="text-sm text-red-600">
        API Gateway unreachable. Make sure the backend stack is running (docker compose up).
      </p>
    );
  }

  return <p className="text-sm text-emerald-600">API Gateway status: {data?.status}</p>;
}
