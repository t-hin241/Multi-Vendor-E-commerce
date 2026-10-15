// Storefront check for the rehearsal (PW-049): the product page shows the
// SKU price range before a choice and the exact SKU price after it, on
// desktop and phone widths. Uses the frontend's Playwright install.
//   node deploy/rehearsal/variant-pricing-ui.mjs <storefront url> <slug> <min> <max> <out dir>
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.join(here, '../../frontend/package.json'));
const { chromium } = require('@playwright/test');

const [origin, slug, min, max, out] = process.argv.slice(2);
if (!out) throw new Error('usage: <storefront url> <slug> <min> <max> <out dir>');
const money = (n) => `${Number(n).toLocaleString('vi-VN')} VND`;
const range = Number(min) === Number(max) ? money(min) : `${money(min)} – ${money(max)}`;

const browser = await chromium.launch();
let failed = false;
try {
  for (const [name, viewport] of [['desktop', { width: 1280, height: 900 }], ['mobile', { width: 390, height: 844 }]]) {
    const page = await browser.newPage({ viewport, locale: 'vi-VN' });
    await page.goto(`${origin}/products/${slug}`, { waitUntil: 'networkidle' });
    const price = page.locator('p.text-3xl').first();
    await price.waitFor({ timeout: 30000 });
    const before = (await price.textContent()).trim();
    await page.screenshot({ path: path.join(out, `storefront-${name}-range.png`), fullPage: false });
    await page.locator('#variant-select').click();
    await page.getByRole('option').first().click();
    const after = (await price.textContent()).trim();
    await page.screenshot({ path: path.join(out, `storefront-${name}-sku.png`), fullPage: false });
    const okRange = before === range;
    // One exact price after the choice; it differs from a real range.
    const okSku = /\d/.test(after) && !after.includes('–') && (Number(min) === Number(max) || after !== before);
    console.log(`${okRange && okSku ? 'PASS' : 'FAIL'} [ui:${name}] range="${before}" expected="${range}" chosen_sku="${after}"`);
    failed ||= !(okRange && okSku);
    await page.close();
  }
} finally {
  await browser.close();
}
process.exitCode = failed ? 1 : 0;
