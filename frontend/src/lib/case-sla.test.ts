import { describe, expect, it } from "vitest";
import { actionDue, workItemURL, type WorkItem } from "./case-sla";

describe("case SLA links and deadlines", () => {
  it("derives the resource link and ignores an injected URL", () => {
    expect(
      workItemURL({
        resource_type: "support",
        resource_id: "case-1",
        url: "https://untrusted.test",
      } as WorkItem),
    ).toBe("/admin/support/case-1");
  });
  it("shows the outer deadline while waiting for the buyer", () => {
    expect(
      actionDue({
        paused_at: "2026-10-07",
        due_at: "2026-10-08",
        overall_due_at: "2026-10-14",
      } as WorkItem),
    ).toBe("2026-10-14");
  });
});
