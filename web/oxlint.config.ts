import { defineConfig } from "oxlint";
import core from "ultracite/oxlint/core";
import react from "ultracite/oxlint/react";
import vitest from "ultracite/oxlint/vitest";

export default defineConfig({
  env: core.env,
  extends: [core, react, vitest],
  ignorePatterns: [
    ...core.ignorePatterns,
    ".storybook/public/mockServiceWorker.js",
  ],
});
