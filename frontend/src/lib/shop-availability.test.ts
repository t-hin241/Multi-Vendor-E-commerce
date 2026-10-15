import { afterEach, describe, expect, it, vi } from "vitest";

import {
  instantToVietnamInput,
  resumeShop,
  scheduleVacation,
  vietnamInputToInstant,
} from "./shop-availability";
import { canOpen } from "./shop-access";

afterEach(() => vi.unstubAllGlobals());

describe("shop vacation", () => {
  it("interprets local input as Vietnam time across the UTC day boundary", () => {
    expect(vietnamInputToInstant("2026-10-14T00:00")).toBe("2026-10-13T17:00:00.000Z");
    expect(instantToVietnamInput("2026-10-13T17:00:00Z")).toBe("2026-10-14T00:00");
    expect(vietnamInputToInstant("")).toBeUndefined();
    expect(instantToVietnamInput(undefined)).toBe("");
  });
  it.each([
    "2026-02-30T08:00",
    "2026-13-01T08:00",
    "2026-10-14T25:00",
    "2026-10-14T08:00Z",
    "yesterday",
  ])("refuses invalid local input %s", (value) => {
    expect(vietnamInputToInstant(value)).toBeNull();
  });
  it("sends the version on both schedule and resume", async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: {} }) });
    vi.stubGlobal("fetch", fetcher);
    await scheduleVacation("fake-token", "shop", { message: "Nghỉ lễ", expected_version: 3 });
    expect(fetcher.mock.calls[0]?.[1]).toMatchObject({
      method: "PUT",
      body: JSON.stringify({ message: "Nghỉ lễ", expected_version: 3 }),
    });
    await resumeShop("fake-token", "shop", 4);
    expect(new URL(fetcher.mock.calls[1]?.[0] ?? "").searchParams.get("expected_version")).toBe(
      "4",
    );
    expect(fetcher.mock.calls[1]?.[1]).toMatchObject({ method: "DELETE" });
  });
  it("opens the vacation page only with the explicit shop permission", () => {
    expect(
      canOpen("/vendor/availability", { role: "staff", capabilities: ["orders.fulfill"] }, "buyer"),
    ).toBe(false);
    expect(
      canOpen(
        "/vendor/availability",
        { role: "staff", capabilities: ["shop.availability.write"] },
        "buyer",
      ),
    ).toBe(true);
  });
});
