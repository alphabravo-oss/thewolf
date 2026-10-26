import { defineConfig, devices } from "@playwright/test";

if (!process.env.WOLF_LIVE_BASE_URL) {
  throw new Error("Run npm run test:e2e:live, or set WOLF_LIVE_BASE_URL to an isolated real Wolf server.");
}
const output = process.env.WOLF_LIVE_RESULTS_DIR ?? "/tmp/wolf-e2e-live-results";

export default defineConfig({
  testDir: "./e2e-live",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  timeout: 600_000,
  expect: { timeout: 15_000 },
  outputDir: `${output}/artifacts`,
  reporter: [["list", { printSteps: true }], ["json", { outputFile: `${output}/report.json` }], ["html", { outputFolder: `${output}/html`, open: "never" }]],
  use: {
    baseURL: process.env.WOLF_LIVE_BASE_URL,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    colorScheme: "dark",
    reducedMotion: "reduce",
    permissions: ["clipboard-read", "clipboard-write"],
  },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "mobile", use: { ...devices["Pixel 7"], viewport: { width: 390, height: 844 } } },
  ],
});
