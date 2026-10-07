import { describe, expect, it } from "vitest";

import type { PermissionDefinition } from "./api-client";
import {
  acceptErrorMessage,
  can,
  canOpen,
  grantablePermissions,
  permissionLabel,
  readInvitationToken,
} from "./shop-access";

const staff = {
  role: "staff" as const,
  capabilities: ["orders.read", "orders.fulfill", "staff.manage"],
};
const owner = {
  role: "owner" as const,
  capabilities: ["products.write", "analytics.read", "payout_destination.write"],
};

describe("shop access", () => {
  it("shows only the pages the active shop allows", () => {
    expect(canOpen("/vendor/orders", staff, "buyer")).toBe(true);
    expect(canOpen("/vendor/products", staff, "buyer")).toBe(false);
    expect(canOpen("/vendor/payout-accounts", staff, "vendor")).toBe(false);
    expect(canOpen("/vendor/payout-accounts", owner, "vendor")).toBe(true);
    expect(canOpen("/vendor/staff", staff, "buyer")).toBe(true);
    // Registering a shop needs a vendor account, not a shop permission.
    expect(canOpen("/vendor/shops", staff, "buyer")).toBe(false);
    expect(canOpen("/vendor/shops", staff, "vendor")).toBe(true);
    expect(canOpen("/vendor/orders", undefined, "vendor")).toBe(false);
    expect(can(staff, "orders.fulfill")).toBe(true);
  });

  it("offers only grantable permissions the manager holds", () => {
    const registry: PermissionDefinition[] = [
      { name: "orders.read", owner_only: false, available: true },
      { name: "products.write", owner_only: false, available: true },
      { name: "marketing.manage", owner_only: false, available: false },
      { name: "payout_destination.write", owner_only: true, available: true },
    ];
    expect(grantablePermissions(registry, staff).map((p) => p.name)).toEqual(["orders.read"]);
    expect(
      grantablePermissions(registry, {
        capabilities: [
          "orders.read",
          "products.write",
          "marketing.manage",
          "payout_destination.write",
        ],
      }).map((p) => p.name),
    ).toEqual(["orders.read", "products.write"]);
  });

  it("reads the invitation token only from a well-formed fragment", () => {
    const token = "A".repeat(42) + "_";
    expect(readInvitationToken(`#token=${token}`)).toBe(token);
    expect(readInvitationToken(`token=${token}`)).toBe(token);
    expect(readInvitationToken("#token=short")).toBeNull();
    expect(readInvitationToken(`#token=${token}<script>`)).toBeNull();
    expect(readInvitationToken("")).toBeNull();
  });

  it("explains acceptance refusals without revealing the invited address", () => {
    expect(acceptErrorMessage("invitation_used", "x")).toMatch(/đã được sử dụng/);
    expect(acceptErrorMessage("permission_denied", "x")).not.toMatch(/@/);
    expect(acceptErrorMessage("other", "fallback")).toBe("fallback");
    expect(permissionLabel("orders.fulfill")).toBe("Xử lý và giao đơn");
    expect(permissionLabel("unknown.permission")).toBe("unknown.permission");
  });
});
