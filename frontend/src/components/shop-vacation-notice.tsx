import { CalendarOff } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import type { PublicShopAvailability } from "@/lib/api-client";
import { formatVietnamTime } from "@/lib/shop-availability";

// ShopVacationNotice tells buyers a shop is on vacation (AF-18), or that one
// is coming. Display only: Cart and Order refuse new orders on their own.
export function ShopVacationNotice({
  availability,
  className,
}: {
  availability?: PublicShopAvailability;
  className?: string;
}) {
  if (!availability) return null;
  if (availability.accepting_orders && !availability.pause_starts_at) return null;
  const paused = !availability.accepting_orders;
  return (
    <Alert className={className}>
      <CalendarOff className="size-4" />
      <AlertTitle>
        {paused
          ? "Shop đang tạm nghỉ, chưa nhận đơn mới"
          : `Shop sẽ tạm nghỉ từ ${formatVietnamTime(availability.pause_starts_at)}`}
      </AlertTitle>
      <AlertDescription>
        {paused &&
          (availability.next_open_at
            ? `Dự kiến mở bán lại: ${formatVietnamTime(availability.next_open_at)}.`
            : "Shop chưa hẹn ngày mở bán lại.")}
        {availability.message && (
          <span className="mt-1 block whitespace-pre-wrap">{availability.message}</span>
        )}
      </AlertDescription>
    </Alert>
  );
}
