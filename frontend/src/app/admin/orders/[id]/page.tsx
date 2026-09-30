"use client";

import { useParams } from "next/navigation";

import { AdminOrderDetail } from "@/components/admin/admin-order-detail";

export default function AdminOrderDetailPage() {
  const params = useParams<{ id: string }>();
  return <AdminOrderDetail orderId={params.id} />;
}
