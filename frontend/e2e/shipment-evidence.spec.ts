import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// PW-038: an admin records a lost package with the carrier's confirmation.
// The file goes to Shipment first; the report cites it, and Shipment's
// refusal without evidence is shown.

let api: MockApi;
const admin: MockUser = {
  id: uuid(31),
  email: "admin.ops@example.test",
  full_name: "Admin Ops",
  role: "admin",
};
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
  "base64",
);

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  api.addUser(admin);
  await api.install(page);
});

test("a lost report carries the carrier's confirmation", async ({ page }) => {
  const now = new Date().toISOString();
  const shipment = {
    id: uuid(700),
    vendor_order_id: uuid(701),
    vendor_id: uuid(80),
    status: "shipped",
    tracking_number: "TRK-700",
    province: "HN",
    failed_attempts: 0,
    version: 3,
    created_at: now,
    updated_at: now,
  };
  api.on("GET", /^\/api\/shipments\/admin\/operations$/, () => ({
    data: { counts: { tracking_stale: 1 }, outbox: [], lists: { tracking_stale: [shipment] } },
  }));
  api.on("POST", /^\/api\/shipments\/admin\/shipments\/([0-9a-f-]{36})\/evidence$/, ({ match }) =>
    match[1] === shipment.id
      ? {
          status: 201,
          data: {
            id: uuid(702),
            content_type: "image/png",
            size_bytes: PNG.length,
            state: "uploaded",
            owner_role: "admin",
            created_at: now,
          },
        }
      : { status: 404, code: "not_found", message: "Shipment not found" },
  );
  api.on(
    "POST",
    /^\/api\/shipments\/admin\/shipments\/([0-9a-f-]{36})\/failure-reports$/,
    ({ body }) => {
      const ids = (body.evidence_ids as string[] | undefined) ?? [];
      if (!ids.length) {
        return {
          status: 422,
          code: "evidence_required",
          message: "Attach the carrier's confirmation (photo or video) to report a package lost",
        };
      }
      return { data: { ...shipment, status: "lost", version: 4 } };
    },
  );

  await login(page, admin.email);
  await page.goto("/admin/fulfillment");
  await page.getByRole("button", { name: "Lost" }).click();
  await page.getByPlaceholder("Reason…").fill("Hãng xác nhận thất lạc, mã TL-1");
  await page.getByRole("button", { name: "Lost" }).last().click();
  await expect(page.getByText(/Attach the carrier's confirmation/)).toBeVisible();

  await page
    .getByLabel("Tệp chứng cứ")
    .setInputFiles({ name: "bien-ban.png", mimeType: "image/png", buffer: PNG });
  await expect(page.getByText("bien-ban.png")).toBeVisible();
  await page.getByRole("button", { name: "Lost" }).last().click();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  const report = api.requests.filter((r) => r.path.endsWith("/failure-reports")).at(-1);
  expect(report?.body).toMatchObject({
    kind: "lost",
    expected_version: 3,
    evidence_ids: [uuid(702)],
  });
});
