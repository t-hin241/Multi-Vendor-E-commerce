import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, createSupportCase, listSupportCases } from "./api-client";
import {
  adminCanResolve,
  buyerCanReply,
  canReopen,
  checkImageFile,
  isCaseActive,
  isOverdue,
  supportErrorMessage,
  supportStatusLabel,
} from "./support-cases";

describe("support case permissions", () => {
  it("lets the buyer reopen only within the window after resolution", () => {
    const resolved = { status: "resolved" as const, resolved_at: "2026-10-01T00:00:00Z" };
    expect(canReopen(resolved, 7, new Date("2026-10-07T23:00:00Z"))).toBe(true);
    expect(canReopen(resolved, 7, new Date("2026-10-08T00:00:01Z"))).toBe(false);
    expect(canReopen({ status: "in_progress", resolved_at: null }, 7)).toBe(false);
  });

  it("stops replies once resolved and polling once closed", () => {
    expect(buyerCanReply({ status: "waiting_buyer" })).toBe(true);
    expect(buyerCanReply({ status: "resolved" })).toBe(false);
    expect(isCaseActive({ status: "resolved" })).toBe(true);
    expect(isCaseActive({ status: "closed" })).toBe(false);
  });

  it("resolves only assigned cases that are not waiting for money", () => {
    expect(adminCanResolve({ status: "open" })).toBe(false);
    expect(adminCanResolve({ status: "waiting_vendor" })).toBe(true);
    expect(adminCanResolve({ status: "resolution_pending" })).toBe(false);
  });

  it("flags overdue cases only while the marketplace must act", () => {
    const now = new Date("2026-10-07T00:00:00Z");
    expect(isOverdue({ status: "in_progress", due_at: "2026-10-06T00:00:00Z" }, now)).toBe(true);
    expect(isOverdue({ status: "resolved", due_at: "2026-10-06T00:00:00Z" }, now)).toBe(false);
    expect(isOverdue({ status: "waiting_buyer", due_at: null }, now)).toBe(false);
  });

  it("words statuses and refusals", () => {
    expect(supportStatusLabel("waiting_vendor")).toBe("Chờ người bán phản hồi");
    expect(supportErrorMessage(new ApiError(409, "case_already_open", "x"), "fb")).toMatch(
      /đang mở/,
    );
    expect(supportErrorMessage(new Error("boom"), "fallback")).toBe("fallback");
  });

  it("checks image type and size before uploading", () => {
    expect(checkImageFile({ type: "image/png", size: 10 }, 100)).toBeNull();
    expect(checkImageFile({ type: "application/pdf", size: 10 }, 100)).toMatch(/JPEG/);
    expect(checkImageFile({ type: "image/jpeg", size: 101 }, 100)).toMatch(/5 MiB/);
  });
});

describe("support case requests", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the idempotency key with a new case", async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { id: "case-1" } }),
      headers: new Headers(),
    });
    vi.stubGlobal("fetch", fetcher);
    await createSupportCase(
      "token",
      "order-1",
      { vendorOrderId: "vo-1", category: "damaged", message: "hỏng", attachmentIds: ["a1"] },
      "key-12345678",
    );
    const [url, init] = fetcher.mock.calls[0] as [
      string,
      { headers: Record<string, string>; body: string },
    ];
    expect(url).toMatch(/\/api\/orders\/order-1\/support-cases$/);
    expect(init.headers["Idempotency-Key"]).toBe("key-12345678");
    expect(JSON.parse(init.body)).toEqual({
      vendor_order_id: "vo-1",
      category: "damaged",
      message: "hỏng",
      attachment_ids: ["a1"],
    });
  });

  it("lists each role from its own prefix", async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { items: [], next_cursor: "" } }),
      headers: new Headers(),
    });
    vi.stubGlobal("fetch", fetcher);
    await listSupportCases("t", "vendor", { vendorId: "v1", status: "open" });
    await listSupportCases("t", "admin", { assignee: "me", overdue: true });
    expect(fetcher.mock.calls[0]?.[0]).toMatch(
      /\/api\/orders\/vendor\/support-cases\?status=open&vendor_id=v1$/,
    );
    expect(fetcher.mock.calls[1]?.[0]).toMatch(
      /\/api\/orders\/admin\/support-cases\?assignee=me&overdue=true$/,
    );
  });
});
