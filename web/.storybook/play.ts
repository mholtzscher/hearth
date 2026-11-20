import { expect } from "storybook/test";

/** A phone story must fit its viewport without horizontal scrolling. */
export const expectNoHorizontalOverflow = async (
  canvasElement: HTMLElement
): Promise<void> => {
  const root = canvasElement.ownerDocument.documentElement;
  await expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);
};

/** Check the visible card's height throughout an interaction, not just at rest. */
export const watchHeight = async (
  element: HTMLElement,
  interact: () => Promise<void>
): Promise<void> => {
  await element.ownerDocument.fonts.ready;
  const height = () => element.getBoundingClientRect().height;
  const initial = height();
  const observed = new Set([initial]);
  const observer = new ResizeObserver(() => {
    observed.add(height());
  });
  observer.observe(element, { box: "border-box" });
  try {
    await interact();
  } finally {
    observer.disconnect();
  }
  observed.add(height());
  await expect([...observed]).toEqual([initial]);
};
