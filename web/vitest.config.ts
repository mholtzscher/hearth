import path from "node:path";

import { storybookTest } from "@storybook/addon-vitest/vitest-plugin";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";

import { resolveChromium } from "./scripts/storybook-browser.ts";

/** Story tests in Playwright Chromium; the plugin includes the stories from `.storybook/main.ts`. */
const storybookProject = (test: {
  name: string;
  exclude?: string[];
  fileParallelism?: boolean;
}) => ({
  extends: false as const,
  plugins: [
    storybookTest({
      configDir: path.resolve(import.meta.dirname, ".storybook"),
      tags: { skip: ["needs-work"] },
    }),
  ],
  test: {
    ...test,
    browser: {
      enabled: true,
      headless: true,
      provider: playwright({
        launchOptions: {
          executablePath: resolveChromium(),
        },
      }),
      instances: [{ browser: "chromium" as const }],
      viewport: { width: 1280, height: 900 },
    },
  },
});

// Each application story runs the whole app with streamed replies and animated
// layout, so the command-line run takes the files in `src/app-stories/` one at
// a time. Storybook's own test runner (its UI and MCP test tools) names its
// project after the config directory and accepts only one, so it gets a single
// project; it usually runs a focused selection.
const storybookProjects =
  process.env.VITEST_STORYBOOK === "true"
    ? [storybookProject({ name: "storybook" })]
    : [
        storybookProject({
          name: "storybook",
          exclude: ["src/app-stories/**"],
        }),
        storybookProject({
          name: "storybook-application",
          exclude: ["src/!(app-stories)/**", "src/*.stories.tsx"],
          fileParallelism: false,
        }),
      ];

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
      ...storybookProjects,
    ],
  },
});
