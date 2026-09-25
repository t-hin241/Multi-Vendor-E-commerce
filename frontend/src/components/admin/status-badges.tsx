import type { VariantProps } from "class-variance-authority";

import { StatusBadge } from "@/components/status-badge";
import type { badgeVariants } from "@/components/ui/badge";
import type { OrderStatus, ProductStatus, RestockStatus, VendorStatus } from "@/lib/api-client";

type Variant = VariantProps<typeof badgeVariants>["variant"];

// English-labeled, deliberately separate from components/vendor/status-badges.tsx
// (Vietnamese-labeled) -- the admin console stays English throughout, per spec
// section 16's "pick one language per area" guidance.

const VENDOR_STATUS_VARIANT: Record<VendorStatus, Variant> = {
  pending: "warning",
  approved: "success",
  rejected: "destructive",
};

export function VendorStatusBadge({ status }: { status: VendorStatus }) {
  return <StatusBadge status={status} variantMap={VENDOR_STATUS_VARIANT} />;
}

const PRODUCT_STATUS_VARIANT: Record<ProductStatus, Variant> = {
  draft: "outline",
  pending_review: "warning",
  approved: "success",
  rejected: "destructive",
};

const PRODUCT_STATUS_LABEL: Record<ProductStatus, string> = {
  draft: "draft",
  pending_review: "pending review",
  approved: "approved",
  rejected: "rejected",
};

export function ProductStatusBadge({ status }: { status: ProductStatus }) {
  return (
    <StatusBadge
      status={status}
      variantMap={PRODUCT_STATUS_VARIANT}
      labelMap={PRODUCT_STATUS_LABEL}
    />
  );
}

const RESTOCK_STATUS_VARIANT: Record<RestockStatus, Variant> = {
  pending: "warning",
  approved: "success",
  rejected: "destructive",
};

export function RestockStatusBadge({ status }: { status: RestockStatus }) {
  return <StatusBadge status={status} variantMap={RESTOCK_STATUS_VARIANT} />;
}

const ORDER_STATUS_VARIANT: Record<OrderStatus, Variant> = {
  pending_payment: "warning",
  paid: "success",
  processing: "info",
  shipped: "info",
  completed: "success",
  cancelled: "outline",
  refunded: "destructive",
};

const ORDER_STATUS_LABEL: Record<OrderStatus, string> = {
  pending_payment: "Pending payment",
  paid: "Paid",
  processing: "Processing",
  shipped: "Shipped",
  completed: "Completed",
  cancelled: "Cancelled",
  refunded: "Refunded",
};

export function OrderStatusBadge({ status }: { status: OrderStatus }) {
  return (
    <StatusBadge status={status} variantMap={ORDER_STATUS_VARIANT} labelMap={ORDER_STATUS_LABEL} />
  );
}
