import { renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useVisibleViewport } from "./visibleViewport";

afterEach(() => vi.unstubAllGlobals());

function fakeViewport() {
  const listeners = new Set<() => void>();
  const viewport = {
    height: 844, offsetTop: 0, scale: 1,
    addEventListener: (_: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
    change(next: Partial<{ height: number; offsetTop: number; scale: number }>) { Object.assign(viewport, next); listeners.forEach(listener => listener()); },
  };
  vi.stubGlobal("visualViewport", viewport);
  vi.stubGlobal("innerHeight", 844);
  return viewport;
}
const style = (name: string) => document.documentElement.style.getPropertyValue(name);

it("fits the app to the area an open keyboard leaves visible", () => {
  const viewport = fakeViewport();
  const { unmount } = renderHook(() => useVisibleViewport());
  expect([style("--visible-height"), style("--visible-top"), style("--safe-bottom")]).toEqual(["844px", "0px", ""]);
  // iOS keeps the page at full height and pans the visible area down to the field.
  viewport.change({ height: 480, offsetTop: 300 });
  expect([style("--visible-height"), style("--visible-top"), style("--safe-bottom")]).toEqual(["480px", "300px", "0px"]);
  // A pinch zoom also shrinks the visible area, but must not resize the app.
  viewport.change({ height: 400, offsetTop: 120, scale: 2 });
  expect([style("--visible-height"), style("--visible-top"), style("--safe-bottom")]).toEqual(["", "", ""]);
  viewport.change({ height: 844, offsetTop: 0, scale: 1 });
  unmount();
  expect(style("--visible-height")).toBe("");
});
