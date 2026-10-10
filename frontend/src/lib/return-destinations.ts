import type { ReturnDestination } from "@/lib/api-client";

// carrierCheckNote describes the carrier's address check of the current
// version (PW-042), or null while there is none.
export function carrierCheckNote(d: ReturnDestination): string | null {
  const c = d.carrier_check;
  if (!c) return null;
  switch (c.result) {
    case "deliverable":
      return d.verified_by_carrier
        ? "Hãng vận chuyển đã xác minh địa chỉ"
        : "Hãng vận chuyển xác nhận phục vụ được địa chỉ";
    case "undeliverable":
      return `Hãng vận chuyển không phục vụ được địa chỉ${c.reason ? `: ${c.reason}` : ""}`;
    case "unsupported":
      return "Hãng vận chuyển không có kiểm tra địa chỉ; sàn sẽ xác minh";
  }
}
