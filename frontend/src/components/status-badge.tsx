import type { VariantProps } from "class-variance-authority";

import { Badge, type badgeVariants } from "@/components/ui/badge";

type BadgeVariant = VariantProps<typeof badgeVariants>["variant"];

// Generic status -> badge pill, configured per status domain (order today;
// payment/shipment/product/vendor/restock can reuse this the same way once
// those surfaces adopt badges too).
export function StatusBadge<T extends string>({
  status,
  variantMap,
  labelMap,
  className,
}: {
  status: T;
  variantMap: Record<T, BadgeVariant>;
  labelMap?: Record<T, string>;
  className?: string;
}) {
  return (
    <Badge variant={variantMap[status]} className={className}>
      {labelMap?.[status] ?? status}
    </Badge>
  );
}
