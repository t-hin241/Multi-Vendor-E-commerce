import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

// The one repeated page-wrapper div (max width + responsive padding) every
// buyer route uses. `lg` = 6xl (home/category/product-detail/cart), `sm` =
// 2xl (orders/order-detail/addresses). Login/register keep their own
// narrower, vertically-centered wrapper -- structurally different, not a fit
// for this component.
export function PageShell({
  maxWidth = "lg",
  className,
  children,
}: {
  maxWidth?: "lg" | "sm";
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      className={cn(
        "mx-auto px-4 py-8 sm:px-6",
        maxWidth === "lg" ? "max-w-6xl" : "max-w-2xl",
        className,
      )}
    >
      {children}
    </div>
  );
}
