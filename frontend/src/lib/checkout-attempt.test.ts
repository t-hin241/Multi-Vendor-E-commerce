import { describe, expect, it } from "vitest";

import { clearAttempt, loadAttempt, nextAttempt } from "./checkout-attempt";

function memory() {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
  };
}

const input = { addressId: "addr-1", cartVersion: 3, expectedTotalAmount: 150000 };

describe("checkout attempt storage", () => {
  it("reuses the key after a reload for the same input", () => {
    const s = memory();
    const first = nextAttempt("user-1", input, 1000, s);
    const again = nextAttempt("user-1", input, 5000, s);
    expect(again.key).toBe(first.key);
  });

  it("starts a new attempt when the cart, address or total changes", () => {
    const s = memory();
    const first = nextAttempt("user-1", input, 1000, s);
    expect(nextAttempt("user-1", { ...input, expectedTotalAmount: 160000 }, 2000, s).key).not.toBe(
      first.key,
    );
  });

  it("is scoped to the user and holds no personal data", () => {
    const s = memory();
    const a = nextAttempt("user-1", input, 1000, s);
    const b = nextAttempt("user-2", input, 1000, s);
    expect(a.key).not.toBe(b.key);
    for (const value of s.data.values()) {
      expect(value).not.toMatch(/token|@|password/i);
    }
  });

  it("expires after a day and is cleared once the order is known", () => {
    const s = memory();
    nextAttempt("user-1", input, 0, s);
    expect(loadAttempt("user-1", 25 * 60 * 60 * 1000, s)).toBeNull();
    nextAttempt("user-1", input, 0, s);
    clearAttempt("user-1", s);
    expect(loadAttempt("user-1", 1, s)).toBeNull();
  });

  it("ignores a damaged record", () => {
    const s = memory();
    s.setItem("shopee.checkout-attempt.user-1", "{not json");
    expect(loadAttempt("user-1", 1, s)).toBeNull();
    s.setItem("shopee.checkout-attempt.user-1", JSON.stringify({ key: 1 }));
    expect(loadAttempt("user-1", 1, s)).toBeNull();
  });
});
