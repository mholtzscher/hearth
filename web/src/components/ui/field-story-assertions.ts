import { expect, waitFor } from "storybook/test";

export async function expectFieldSize(field: HTMLElement, height: "32px" | "44px") {
  const style = getComputedStyle(field);
  await expect(style.height).toBe(height);
  await expect(style.borderRadius).toBe("8px");
  await expect(style.fontSize).toBe("14px");
}

export async function expectFieldFocus(field: HTMLElement) {
  await expect(field).toHaveFocus();
  const style = getComputedStyle(field);
  await expect(style.outlineWidth).toBe("2px");
  await expect(style.outlineStyle).toBe("solid");
  await expect(style.outlineOffset).toBe("3px");
  await waitFor(() => expect(getComputedStyle(field).outlineColor).toBe("rgb(167, 123, 255)"));
}

/** The editor's 6px corners are the one approved difference at touch size. */
export async function expectHouseholdField(shared: HTMLElement, legacy: HTMLElement) {
  await shared.ownerDocument.fonts.ready;
  const current = getComputedStyle(shared);
  const reference = getComputedStyle(legacy);
  const properties = [
    "height", "width", "backgroundColor", "color", "borderColor", "borderWidth",
    "borderStyle", "fontFamily", "fontSize", "fontWeight", "lineHeight", "padding", "opacity",
  ] as const;
  for (const property of properties) {
    await expect(current[property], property).toBe(reference[property]);
  }
  await expect(reference.borderRadius).toBe("6px");
  await expect(current.borderRadius).toBe("8px");
}
