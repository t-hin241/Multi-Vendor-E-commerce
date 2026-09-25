import type { VariantProps } from "class-variance-authority";

import { StatusBadge } from "@/components/status-badge";
import type { badgeVariants } from "@/components/ui/badge";
import type { ProductStatus, RestockStatus, VendorStatus } from "@/lib/api-client";

type Variant = VariantProps<typeof badgeVariants>["variant"];

const VENDOR_STATUS_VARIANT: Record<VendorStatus, Variant> = {
  pending: "warning",
  approved: "success",
  rejected: "destructive",
};

const VENDOR_STATUS_LABEL: Record<VendorStatus, string> = {
  pending: "Chờ duyệt",
  approved: "Đã duyệt",
  rejected: "Bị từ chối",
};

export function VendorStatusBadge({ status }: { status: VendorStatus }) {
  return (
    <StatusBadge status={status} variantMap={VENDOR_STATUS_VARIANT} labelMap={VENDOR_STATUS_LABEL} />
  );
}

const PRODUCT_STATUS_VARIANT: Record<ProductStatus, Variant> = {
  draft: "outline",
  pending_review: "warning",
  approved: "success",
  rejected: "destructive",
};

const PRODUCT_STATUS_LABEL: Record<ProductStatus, string> = {
  draft: "Nháp",
  pending_review: "Chờ duyệt",
  approved: "Đã duyệt",
  rejected: "Bị từ chối",
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

const RESTOCK_STATUS_LABEL: Record<RestockStatus, string> = {
  pending: "Chờ duyệt",
  approved: "Đã duyệt",
  rejected: "Bị từ chối",
};

export function RestockStatusBadge({ status }: { status: RestockStatus }) {
  return (
    <StatusBadge
      status={status}
      variantMap={RESTOCK_STATUS_VARIANT}
      labelMap={RESTOCK_STATUS_LABEL}
    />
  );
}
