import { describe, expect, it } from "vitest";

import { ApiError, type InboxItem } from "@/lib/api-client";
import {
  inboxDisabled,
  inboxErrorMessage,
  newestId,
  safeLink,
  timeAgo,
  unreadLabel,
} from "@/lib/inbox";

const item = (id: string): InboxItem => ({
  id,
  type: "order_paid",
  title: "t",
  body: "b",
  reference_type: "order",
  reference_id: "o",
  link: "/orders/o",
  read_at: null,
  created_at: "2026-10-09T08:00:00Z",
});

describe("safeLink", () => {
  it("keeps app routes and refuses anything else", () => {
    expect(safeLink("/orders/abc")).toBe("/orders/abc");
    expect(safeLink("/admin/returns?return_id=1")).toBe("/admin/returns?return_id=1");
    expect(safeLink("https://evil.example")).toBe("/notifications");
    expect(safeLink("//evil.example")).toBe("/notifications");
    expect(safeLink("/\\evil.example")).toBe("/notifications");
    expect(safeLink("javascript:alert(1)")).toBe("/notifications");
  });
});

describe("labels", () => {
  it("caps the badge", () => {
    expect(unreadLabel(0)).toBe("");
    expect(unreadLabel(7)).toBe("7");
    expect(unreadLabel(120)).toBe("99+");
  });
  it("formats relative time", () => {
    const now = new Date("2026-10-09T10:00:00Z");
    expect(timeAgo("2026-10-09T09:59:30Z", now)).toBe("Vừa xong");
    expect(timeAgo("2026-10-09T09:55:00Z", now)).toBe("5 phút trước");
    expect(timeAgo("2026-10-09T07:00:00Z", now)).toBe("3 giờ trước");
    expect(timeAgo("2026-10-07T10:00:00Z", now)).toBe("2 ngày trước");
  });
  it("recognises the disabled feature and a missing item", () => {
    expect(inboxDisabled(new ApiError(404, "feature_disabled", "off"))).toBe(true);
    expect(inboxDisabled(new ApiError(404, "not_found", "x"))).toBe(false);
    expect(inboxErrorMessage(new ApiError(404, "not_found", "x"))).toContain("không còn tồn tại");
  });
  it("marks read through the newest loaded item", () => {
    expect(newestId(undefined)).toBeNull();
    expect(newestId([{ items: [] }])).toBeNull();
    expect(newestId([{ items: [item("a"), item("b")] }, { items: [item("c")] }])).toBe("a");
  });
});
