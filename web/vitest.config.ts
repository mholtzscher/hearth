import path from "node:path";

import { storybookTest } from "@storybook/addon-vitest/vitest-plugin";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    projects: [
      {
        extends: "./vite.config.ts",
        test: { name: "unit", include: ["src/**/*.test.{ts,tsx}"] },
      },
      {
        extends: false,
        test: {
          name: "scripts",
          environment: "node",
          include: ["scripts/**/*.test.ts"],
        },
      },
      {
        extends: false,
        plugins: [
          storybookTest({
            configDir: path.resolve(import.meta.dirname, ".storybook"),
            tags: { skip: ["needs-work"] },
          }),
        ],
        test: {
          name: "storybook",
          browser: {
            enabled: true,
            headless: true,
            provider: playwright({
              launchOptions: {
                executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
              },
            }),
            instances: [{ browser: "chromium" }],
            viewport: { width: 1280, height: 900 },
          },
        },
      },
    ],
  },
});
