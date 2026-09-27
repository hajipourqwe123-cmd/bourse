import { defineConfig } from "@playwright/test";

// Gate 2 only (`make gate2` starts the stack and sets GATE_URL). Not part of `npm test`.
export default defineConfig({
  testDir: "tests",
  timeout: 15 * 60_000,
  workers: 1,
  reporter: [["list"]],
  outputDir: "../gate2-out/playwright",
  use: {
    baseURL: process.env.GATE_URL ?? "http://127.0.0.1:8090/",
    locale: "fa-IR",
    timezoneId: "Asia/Tehran",
    launchOptions: process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {},
  },
});
