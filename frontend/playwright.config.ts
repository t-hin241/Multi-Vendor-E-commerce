import { defineConfig, devices } from "@playwright/test";

// Browser end-to-end tests of the frontend against a mocked gateway
// (e2e/mock-api.ts): checkout idempotency, payment return, session
// isolation and HTML safety. The app is built with the mock API origin
// and served on :3100. E2E_CHANNEL=chrome uses an installed Chrome instead
// of Playwright's own Chromium.
export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: "http://localhost:3100",
    trace: "retain-on-failure",
    channel: process.env.E2E_CHANNEL || undefined,
  },
  projects: [
    {
      name: "desktop",
      use: { ...devices["Desktop Chrome"], channel: process.env.E2E_CHANNEL || undefined },
      testIgnore: /mobile\.spec\.ts/,
    },
    {
      name: "mobile",
      use: { ...devices["Pixel 7"], channel: process.env.E2E_CHANNEL || undefined },
      testMatch: /mobile\.spec\.ts/,
    },
  ],
  webServer: {
    command: "npm run build && npm run start -- -p 3100",
    url: "http://localhost:3100",
    timeout: 300_000,
    reuseExistingServer: !process.env.CI,
    env: { NEXT_PUBLIC_API_BASE_URL: "http://127.0.0.1:3999" },
  },
});
