import type { VariantProps } from "class-variance-authority";

import { StatusBadge } from "@/components/status-badge";
import type { badgeVariants } from "@/components/ui/badge";
import type { OrderStatus } from "@/lib/api-client";

const ORDER_STATUS_VARIANT: Record<OrderStatus, VariantProps<typeof badgeVariants>["variant"]> = {
  pending_payment: "warning",
  paid: "success",
  processing: "info",
  shipped: "info",
  completed: "success",
  cancelled: "outline",
  refunded: "destructive",
};

const ORDER_STATUS_LABEL: Record<OrderStatus, string> = {
  pending_payment: "Chờ thanh toán",
  paid: "Đã thanh toán",
  processing: "Đang xử lý",
  shipped: "Đang giao",
  completed: "Hoàn tất",
  cancelled: "Đã hủy",
  refunded: "Đã hoàn tiền",
};

export function OrderStatusBadge({ status }: { status: OrderStatus }) {
  return <StatusBadge status={status} variantMap={ORDER_STATUS_VARIANT} labelMap={ORDER_STATUS_LABEL} />;
}
