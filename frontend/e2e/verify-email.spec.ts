import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi } from "./mock-api";

// PW-022: the email link carries its token in the fragment; the page sends
// it only in the request body, once. A staff invitation refused for an
// unconfirmed email offers to send the confirmation again.

let api: MockApi;

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

test("the link confirms the email once and keeps the token out of URLs", async ({ page }) => {
  const token = "T".repeat(43);
  let used = false;
  api.on("POST", /^\/api\/auth\/email-verifications\/confirmations$/, ({ body }) => {
    if (body.token !== token || used) {
      return {
        status: 400,
        code: "invalid_token",
        message: "This link is invalid or has expired; ask for a new one",
      };
    }
    used = true;
    return { data: { verified: true } };
  });
  await page.goto(`/verify-email#token=${token}`);
  await expect(page.getByRole("button", { name: "Xác nhận email" })).toBeVisible();
  expect(page.url()).not.toContain(token);
  await page.getByRole("button", { name: "Xác nhận email" }).click();
  await expect(page.getByText("Địa chỉ email đã được xác nhận.")).toBeVisible();
  expect(api.sent("POST", /confirmations$/)).toEqual([{ token }]);
  expect(api.requests.some((r) => r.path.includes(token))).toBe(false);

  await page.goto("/");
  await page.goto(`/verify-email#token=${token}`);
  await page.getByRole("button", { name: "Xác nhận email" }).click();
  await expect(page.getByRole("main").getByRole("alert")).toContainText(
    "This link is invalid or has expired",
  );
});

test("an invitation needs a confirmed email and the confirmation can be sent again", async ({
  page,
}) => {
  const staff = api.user("buyer.b@example.test");
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: { shops: [], staff_enabled: true },
  }));
  api.on("POST", /^\/api\/vendor\/staff-invitations\/accept$/, () => ({
    status: 403,
    code: "email_not_verified",
    message: "Confirm your email address first",
  }));
  api.on("POST", /^\/api\/auth\/email-verifications$/, ({ me }) =>
    me?.id === staff.id
      ? { status: 202, data: { sent: true } }
      : { status: 401, code: "unauthorized", message: "Sign in" },
  );
  await login(page, staff.email);
  await page.goto(`/staff-invitations/accept#token=${"A".repeat(43)}`);
  await page.getByRole("button", { name: "Chấp nhận lời mời" }).click();
  await expect(page.getByRole("main").getByRole("alert")).toContainText(
    "Hãy xác nhận địa chỉ email",
  );
  await page.getByRole("button", { name: "Gửi email xác nhận" }).click();
  await expect(page.getByText(/Đã gửi email xác nhận tới buyer\.b@example\.test/)).toBeVisible();
  expect(api.sent("POST", /email-verifications$/)).toHaveLength(1);
});
