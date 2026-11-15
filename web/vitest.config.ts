import path from "node:path";
import { defineConfig } from "vitest/config";
import { storybookTest } from "@storybook/addon-vitest/vitest-plugin";
import { playwright } from "@vitest/browser-playwright";
import { storyShot } from "./scripts/story-shot-command.ts";

/** `story-shots capture` sets this to its output directory. See scripts/story-shots.ts. */
const capturing = Boolean(process.env.VITE_HEARTH_STORY_SHOTS);

export default defineConfig({
  test: {
    projects: [
      {
        extends: "./vite.config.ts",
        test: { name: "unit", include: ["src/**/*.test.{ts,tsx}"] },
      },
      {
        extends: false,
        plugins: [storybookTest({ configDir: path.resolve(import.meta.dirname, ".storybook"), tags: { skip: ["needs-work"] } })],
        test: {
          name: "storybook",
          browser: {
            enabled: true,
            headless: true,
            provider: playwright({
              launchOptions: {
                executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
                // Partial raster repaints part of a tile over its old pixels, which leaves
                // 1/255 differences on anti-aliased edges between two captures of one commit.
                args: capturing ? ["--disable-partial-raster"] : [],
              },
              // Components that respect reduced motion render their non-animated state.
              contextOptions: capturing ? { reducedMotion: "reduce" } : {},
            }),
            instances: [{ browser: "chromium" }],
            viewport: { width: 1280, height: 900 },
            // Called by .storybook/story-capture.ts, only during `story-shots capture`.
            commands: { storyShot },
          },
        },
      },
    ],
  },
});
