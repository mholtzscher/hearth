import { expect, waitFor } from "storybook/test";

export const expectFieldSize = async (
  field: HTMLElement,
  height: "32px" | "44px"
) => {
  const style = getComputedStyle(field);
  await expect(style.height).toBe(height);
  await expect(style.borderRadius).toBe("8px");
  await expect(style.fontSize).toBe("14px");
};

export const expectFieldFocus = async (field: HTMLElement) => {
  await expect(field).toHaveFocus();
  const style = getComputedStyle(field);
  await expect(style.outlineWidth).toBe("2px");
  await expect(style.outlineStyle).toBe("solid");
  await expect(style.outlineOffset).toBe("3px");
  await waitFor(() =>
    expect(getComputedStyle(field).outlineColor).toBe("rgb(167, 123, 255)")
  );
};
