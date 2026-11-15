import type { StoryContext } from "@storybook/react-vite";

declare module "vitest/browser" {
  interface BrowserCommands {
    /** Saves a screenshot of the story document and records the class names it rendered. */
    storyShot: (name: string, classes: string[]) => Promise<void>;
  }
}

const PHONE = { width: 390, height: 844 };
const DESKTOP = { width: 1280, height: 900 };

const nextFrame = () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));

/** Load the theme font before native selects render their selected text. */
export async function prepareCapture({ globals }: Pick<StoryContext, "globals">) {
  const { page } = await import("vitest/browser");
  // The addon otherwise overrides Vitest's configured default with 1200×900.
  if (!globals.viewport?.value) await page.viewport(DESKTOP.width, DESKTOP.height);
  await document.fonts.load(getComputedStyle(document.body).font);
}

/**
 * Resolves once nothing is marked busy and the document has not changed for
 * `quiet` ms, or after `limit` ms. This lets pending requests and React updates
 * finish after the play function. A story that stays busy on purpose is captured
 * at the limit.
 */
function settled(quiet = 150, limit = 3000): Promise<void> {
  return new Promise(resolve => {
    const finish = () => {
      observer.disconnect();
      clearTimeout(idle);
      clearTimeout(deadline);
      resolve();
    };
    // While something is busy, the mutation that clears it starts the next quiet period.
    const check = () => { if (!document.querySelector('[aria-busy="true"]')) finish(); };
    const observer = new MutationObserver(() => {
      clearTimeout(idle);
      idle = setTimeout(check, quiet);
    });
    let idle = setTimeout(check, quiet);
    const deadline = setTimeout(finish, limit);
    observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
  });
}

/**
 * Temporary, for the design-system migration: screenshots a finished story so
 * that scripts/story-shots.ts can compare two revisions. The preview calls it
 * only when VITE_HEARTH_STORY_SHOTS names an output directory.
 *
 * A story that sets its own viewport is captured at that size. Any other story
 * is captured at 1280×900 and again at the phone width.
 */
export async function captureStory({ id, globals }: Pick<StoryContext, "id" | "globals">) {
  const { commands, page } = await import("vitest/browser");
  const story = { width: window.innerWidth, height: window.innerHeight };
  const shoot = async (name: string) => {
    await document.fonts.ready;
    await settled();
    await nextFrame();
    const classes = new Set<string>();
    for (const element of document.querySelectorAll("[class]")) for (const className of element.classList) classes.add(className);
    await commands.storyShot(name, [...classes].sort());
  };
  try {
    await shoot(id);
    if (!globals.viewport?.value) {
      await page.viewport(PHONE.width, PHONE.height);
      await shoot(`${id}--phone`);
    }
  } finally {
    // The accessibility check runs after this hook and must see the story at its own size.
    await page.viewport(story.width, story.height);
    await nextFrame();
  }
}
