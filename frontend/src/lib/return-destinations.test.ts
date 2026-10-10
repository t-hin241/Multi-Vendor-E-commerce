import { describe, expect, it } from "vitest";

import type { ReturnDestination } from "@/lib/api-client";
import { carrierCheckNote } from "@/lib/return-destinations";

const base: ReturnDestination = {
  vendor_id: "v",
  address_id: "a",
  recipient_name: "Test",
  phone: "0900000000",
  province: "P",
  district: "D",
  ward: "W",
  street_address: "S",
  receiving_hours: "8-17",
  version: 2,
  verified: false,
  updated_at: "2026-10-10T00:00:00Z",
};

describe("carrierCheckNote", () => {
  it("is empty without a check", () => {
    expect(carrierCheckNote(base)).toBeNull();
  });
  it("says who verified a deliverable address", () => {
    const checked = { result: "deliverable" as const, checked_at: "2026-10-10T00:00:00Z" };
    expect(
      carrierCheckNote({
        ...base,
        verified: true,
        verified_by_carrier: true,
        carrier_check: checked,
      }),
    ).toBe("Hãng vận chuyển đã xác minh địa chỉ");
    expect(carrierCheckNote({ ...base, verified: true, carrier_check: checked })).toBe(
      "Hãng vận chuyển xác nhận phục vụ được địa chỉ",
    );
  });
  it("carries the carrier's reason", () => {
    expect(
      carrierCheckNote({
        ...base,
        carrier_check: {
          result: "undeliverable",
          reason: "Ngoài vùng",
          checked_at: "2026-10-10T00:00:00Z",
        },
      }),
    ).toBe("Hãng vận chuyển không phục vụ được địa chỉ: Ngoài vùng");
  });
});
