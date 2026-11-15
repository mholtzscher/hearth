import { expect } from "storybook/test";

/** A phone story must fit its viewport without horizontal scrolling. */
export const expectNoHorizontalOverflow = async (
  canvasElement: HTMLElement
): Promise<void> => {
  const root = canvasElement.ownerDocument.documentElement;
  await expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);
};
