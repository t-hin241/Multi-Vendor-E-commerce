import { expect, test, type Page } from "@playwright/test";

const API = "http://localhost:3999";
const password = "synthetic-browser-password-123";

test("profile conflict is visible and switching accounts clears private data", async ({ page }) => {
  const { token } = await register(page);
  await page.getByLabel("Họ tên", { exact: true }).fill("Unsaved name");
  const res = await page.request.patch(`${API}/api/auth/me`, {
    headers: {
      Origin: "http://localhost:3100",
      "X-CSRF-Protection": "1",
      Authorization: `Bearer ${token}`,
    },
    data: { full_name: "Concurrent name", phone: "+84911111111", expected_version: 1 },
  });
  expect(res.status()).toBe(200);
  await page.getByRole("button", { name: "Lưu hồ sơ", exact: true }).click();
  await expect(page.getByRole("main").getByRole("alert")).toContainText("Profile changed");
  await page.getByRole("button", { name: "Tải lại hồ sơ", exact: true }).click();
  await expect(page.getByLabel("Họ tên", { exact: true })).toHaveValue("Concurrent name");
  await page.getByRole("button", { name: "Đăng xuất tất cả thiết bị" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await register(page, "vendor");
  await expect(page.getByLabel("Họ tên", { exact: true })).toHaveValue("AF12 vendor");
  await expect(page.getByLabel("Số điện thoại")).toHaveValue("");
});

test("revoking another session leaves the current session active", async ({ page }) => {
  const { email, token } = await register(page);
  const second = await page.request.post(`${API}/api/auth/login`, {
    headers: { Origin: "http://localhost:3100", "X-CSRF-Protection": "1" },
    data: { email, password },
  });
  expect(second.status()).toBe(200);
  await page.reload();
  await page.getByRole("button", { name: "Thu hồi", exact: true }).click();
  await expect(page.getByText("Phiên khác", { exact: true })).toHaveCount(0);
  const res = await page.request.get(`${API}/api/auth/me`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(res.status()).toBe(401);
  await expect(page.getByText("Phiên hiện tại", { exact: true })).toBeVisible();
});

async function register(page: Page, role: "buyer" | "vendor" | "admin" = "buyer") {
  const email =
    role === "admin" ? "af12-admin@example.invalid" : `af12-${role}-${Date.now()}@example.invalid`;
  const response = await page.request.post(
    `${API}/api/auth/${role === "admin" ? "login" : "register"}`,
    {
      headers: { Origin: "http://localhost:3100", "X-CSRF-Protection": "1" },
      data: { email, password, full_name: `AF12 ${role}`, role },
    },
  );
  expect(response.status()).toBe(role === "admin" ? 200 : 201);
  const { data } = await response.json();
  await page.goto("/account");
  await expect(page.getByRole("heading", { name: "Tài khoản của tôi" })).toBeVisible();
  return { email, token: data.access_token as string };
}

async function mailbox(page: Page, token: string, kind: string) {
  const response = await page.request.post(`${API}/test/mailbox?kind=${kind}`, {
    headers: {
      Origin: "http://localhost:3100",
      "X-CSRF-Protection": "1",
      Authorization: `Bearer ${token}`,
    },
  });
  expect(response.status()).toBe(200);
  return (await response.json()).url as string;
}

for (const role of ["buyer", "vendor", "admin"] as const) {
  test(`${role} saves a versioned profile and password change revokes access`, async ({ page }) => {
    const { token } = await register(page, role);
    await page.getByLabel("Họ tên", { exact: true }).fill(`Updated ${role}`);
    await page.getByLabel("Số điện thoại").fill("+84912345678");
    await page.getByRole("button", { name: "Lưu hồ sơ", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Đã lưu hồ sơ");
    await page.reload();
    await expect(page.getByLabel("Họ tên", { exact: true })).toHaveValue(`Updated ${role}`);
    await page.getByLabel("Mật khẩu hiện tại", { exact: true }).fill(password);
    await page.getByLabel("Mật khẩu mới", { exact: true }).fill("synthetic-new-browser-password");
    await page.getByRole("button", { name: "Đổi mật khẩu và đăng xuất" }).click();
    await expect(page).toHaveURL(/\/login$/);
    const revoked = await page.request.get(`${API}/api/auth/me`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(revoked.status()).toBe(401);
    const storage = await page.evaluate(() =>
      JSON.stringify({ ...localStorage, ...sessionStorage }),
    );
    expect(storage).not.toContain(token);
  });
}

test("email verification and email change use one-time fragments with real Identity", async ({
  page,
}) => {
  const { token } = await register(page);
  const verifyURL = await mailbox(page, token, "email_verification");
  await page.goto(verifyURL);
  await expect(page.getByRole("button", { name: "Xác nhận email", exact: true })).toBeVisible();
  expect(page.url()).not.toContain("#");
  await page.getByRole("button", { name: "Xác nhận email", exact: true }).click();
  await expect(page.getByText("Địa chỉ email đã được xác nhận.")).toBeVisible();
  await page.goto("/account");
  await expect(page.getByText("Đã xác minh", { exact: true })).toBeVisible();
  const nextEmail = `changed-${Date.now()}@example.invalid`;
  await page.getByLabel("Email mới", { exact: true }).fill(nextEmail);
  await page.getByLabel("Mật khẩu xác nhận đổi email").fill(password);
  await page.getByRole("button", { name: "Gửi xác nhận đổi email" }).click();
  await expect(page.getByRole("status")).toContainText("Kiểm tra địa chỉ email mới");
  const changeURL = await mailbox(page, token, "email_change");
  await page.goto(changeURL);
  await expect(page.getByRole("button", { name: "Xác nhận đổi email", exact: true })).toBeVisible();
  expect(page.url()).not.toContain("#");
  await page.getByRole("button", { name: "Xác nhận đổi email", exact: true }).click();
  await expect(
    page.getByText("Email đã thay đổi. Hãy đăng nhập lại bằng địa chỉ mới."),
  ).toBeVisible();
  await page.goto("/login");
  await page.goto(changeURL);
  await page.getByRole("button", { name: "Xác nhận đổi email", exact: true }).click();
  await expect(page.getByRole("main").getByRole("alert")).toContainText("Unable to change email");
  const signedIn = await page.request.post(`${API}/api/auth/login`, {
    headers: { Origin: "http://localhost:3100", "X-CSRF-Protection": "1" },
    data: { email: nextEmail, password },
  });
  expect(signedIn.status()).toBe(200);
});
