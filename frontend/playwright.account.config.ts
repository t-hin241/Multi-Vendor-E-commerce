import { defineConfig, devices } from "@playwright/test";

// Requires the repo's isolated deploy/identity-test.compose.yml PostgreSQL
// and Redis. No existing development account or database is used.
export default defineConfig({
  globalTeardown: "./e2e-live/account-teardown.ts",
  testDir: "./e2e-live",
  testMatch: "account.spec.ts",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  workers: 1,
  use: {
    baseURL: "http://localhost:3100",
    ...devices["Desktop Chrome"],
    channel: process.env.E2E_CHANNEL || undefined,
    trace: "off",
  },
  webServer: [
    {
      command:
        "go test ./services/identity/internal/repository -run ^TestAccountBrowserServer$ -count=1 -timeout=10m -args -account-browser-server",
      cwd: "../backend",
      url: "http://127.0.0.1:3999/healthz",
      timeout: 120_000,
      reuseExistingServer: false,
      env: {
        GOCACHE: `${process.cwd()}/../.cache/go-build`,
        IDENTITY_TEST_DATABASE_URL:
          "postgres://identity_test@localhost:55431/identity_test?sslmode=disable",
        IDENTITY_TEST_REDIS_URL: "redis://localhost:56391/0",
      },
    },
    {
      command:
        process.env.ACCOUNT_E2E_REUSE_BUILD === "true"
          ? "npm run start -- -p 3100"
          : "npm run build && npm run start -- -p 3100",
      url: "http://localhost:3100",
      timeout: 300_000,
      reuseExistingServer: false,
      env: { NEXT_PUBLIC_API_BASE_URL: "http://localhost:3999" },
    },
  ],
});
