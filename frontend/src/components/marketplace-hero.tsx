import { Headset, ShieldCheck, Store, Truck } from "lucide-react";
import type { ReactNode } from "react";

const TRUST_CUES = [
  { icon: Truck, label: "Giao hàng toàn quốc" },
  { icon: ShieldCheck, label: "Thanh toán an toàn" },
  { icon: Store, label: "Nhiều gian hàng" },
  { icon: Headset, label: "Hỗ trợ đơn hàng" },
];

// Compact commercial hero for the buyer homepage -- not a marketing landing
// page, just enough to signal "real marketplace" above the fold.
export function MarketplaceHero({ statusBadge }: { statusBadge?: ReactNode }) {
  return (
    <div className="relative overflow-hidden rounded-xl border bg-accent/40 p-6 sm:p-8">
      {statusBadge && <div className="absolute top-4 right-4">{statusBadge}</div>}
      <h1 className="max-w-xl text-2xl font-semibold tracking-tight sm:text-3xl">
        Mua sắm từ hàng trăm gian hàng, giá tốt mỗi ngày
      </h1>
      <p className="mt-2 max-w-xl text-sm text-muted-foreground">
        Tìm sản phẩm bạn cần, so sánh giá và đặt hàng chỉ trong vài bước.
      </p>
      <div className="mt-5 flex flex-wrap gap-x-6 gap-y-2">
        {TRUST_CUES.map(({ icon: Icon, label }) => (
          <div key={label} className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Icon className="size-4 text-primary" />
            <span>{label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
