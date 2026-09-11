"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

export default function OrdersPage() {
  const { user, isReady, callWithAuth } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const ordersQuery = useQuery({
    queryKey: ["orders-mine"],
    queryFn: () => callWithAuth((token) => api.listMyOrders(token)),
    enabled: Boolean(user && user.role === "buyer"),
  });

  if (!user || user.role !== "buyer") return null;

  return (
    <main className="mx-auto max-w-2xl px-6 py-10">
      <h1 className="text-2xl font-semibold text-slate-900">My orders</h1>

      {ordersQuery.isPending && <p className="mt-6 text-sm text-slate-500">Loading…</p>}
      {ordersQuery.data?.length === 0 && (
        <p className="mt-6 text-sm text-slate-500">No orders yet.</p>
      )}

      <ul className="mt-6 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {ordersQuery.data?.map((order) => (
          <li key={order.id} className="flex items-center justify-between p-4">
            <div>
              <Link
                href={`/orders/${order.id}`}
                className="font-medium text-slate-900 hover:underline"
              >
                Order #{order.id.slice(0, 8)}
              </Link>
              <p className="text-sm text-slate-600">
                {new Date(order.created_at).toLocaleString()}
              </p>
            </div>
            <div className="text-right">
              <p className="text-sm font-medium capitalize text-slate-900">
                {order.status.replace("_", " ")}
              </p>
              <p className="text-sm text-slate-600">
                {formatMoney(order.total_amount, order.currency)}
              </p>
            </div>
          </li>
        ))}
      </ul>
    </main>
  );
}
