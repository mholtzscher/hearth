import { chromium } from "playwright";
import { parseArgs } from "node:util";
import { browserURL, findChromium, readConfig, checkIndex } from "./storybook-server.ts";
import { installStoryObserver, type StoryObserverScope } from "./storybook-observer.ts";

const { values } = parseArgs({
  options: {
    url: { type: "string" },
    story: { type: "string" },
  },
});

const url = (values.url ?? browserURL(readConfig())).replace(/\/$/, "");
const story = values.story ?? await checkIndex(url);
const browser = await chromium.launch({ executablePath: findChromium(), headless: true });
const page = await browser.newPage();
const errors: string[] = [];
try {
  page.on("pageerror", error => errors.push(error.message));
  await page.addInitScript(installStoryObserver);
  const preview = `${url}/iframe.html?id=${encodeURIComponent(story)}&viewMode=story`;
  const response = await page.goto(preview, { waitUntil: "domcontentloaded", timeout: 45_000 });
  if (!response?.ok()) throw new Error(`Preview returned HTTP ${response?.status()}`);
  // A visible story can still have a caught play error; require its success event.
  await page.waitForFunction(() => {
    const result = (window as unknown as StoryObserverScope).__HEARTH_STORY_CHECK__;
    return result?.status !== null || result?.errors.length;
  }, undefined, { timeout: 45_000 });
  const result = await page.evaluate(() => (window as unknown as StoryObserverScope).__HEARTH_STORY_CHECK__);
  if (result.status !== "success" || result.errors.length) {
    throw new Error(`Storybook reported ${result.status ?? "failure"}: ${result.errors.join(", ")}`);
  }
  if (!await page.locator("body").evaluate(body =>
    body.classList.contains("sb-show-main") && !!body.querySelector("#storybook-root")?.children.length,
  )) {
    throw new Error("Storybook reported a story render, play, or cleanup error.");
  }
  if (errors.length) throw new Error(`Page errors: ${errors.join("; ")}`);
  console.log(`Rendered: ${preview}`);
  console.log("Page errors: 0");
} catch (error) {
  console.error(`Preview diagnostics: ${(await page.locator("body").innerText()).slice(0, 2000)}`);
  console.error(`Page errors: ${errors.join("; ")}`);
  throw error;
} finally {
  await browser.close();
}
