import { writeFileSync } from "node:fs";
import path from "node:path";
import type { BrowserCommand } from "vitest/node";

/** The class names each captured story rendered, written to classes.json after every capture. */
const rendered: Record<string, string[]> = {};

/**
 * Temporary, for the design-system migration: the Node half of
 * .storybook/story-capture.ts. Playwright freezes CSS animations and transitions
 * for the screenshot, so the arrival fade and the pending pulse do not depend
 * on timing.
 */
export const storyShot: BrowserCommand<[name: string, classes: string[]]> = async ({ iframe }, name, classes) => {
  const directory = process.env.VITE_HEARTH_STORY_SHOTS;
  if (!directory) throw new Error("storyShot needs VITE_HEARTH_STORY_SHOTS to name an output directory");
  // Capture the iframe viewport, not a full-height body that changes fixed/vh layouts.
  await iframe.owner().screenshot({ path: path.join(directory, `${name}.png`), animations: "disabled" });
  rendered[name] = classes;
  writeFileSync(path.join(directory, "classes.json"), JSON.stringify(rendered, null, 1));
};
