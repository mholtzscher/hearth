import { expect } from "storybook/test";

/** A phone story must fit its viewport without horizontal scrolling. */
export async function expectNoHorizontalOverflow(canvasElement: HTMLElement) {
  const root = canvasElement.ownerDocument.documentElement;
  await expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);
}
