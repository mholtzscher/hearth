import { useEffect } from "react";

/** The keyboard, not browser chrome, is the only thing that hides this much of the screen. */
const KEYBOARD_MIN_HEIGHT = 120;

/**
 * iOS Safari keeps the page at full height when the on-screen keyboard opens
 * and pans the visible area down to the focused field, which scrolls the top
 * bar away. Publish the visible area as --visible-height and --visible-top so
 * the app can fit and follow it, and clear --safe-bottom while the keyboard
 * covers the home indicator. Android resizes the page itself
 * (interactive-widget in index.html), so there the visible area is the page.
 */
export function useVisibleViewport() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const root = document.documentElement.style;
    function clear() {
      for (const name of ["--visible-height", "--visible-top", "--safe-bottom"]) root.removeProperty(name);
    }
    function sync() {
      // A pinch zoom also shrinks the visible area; only the keyboard should resize the app.
      if (!viewport || Math.abs(viewport.scale - 1) > 0.01) { clear(); return; }
      root.setProperty("--visible-height", `${viewport.height}px`);
      root.setProperty("--visible-top", `${viewport.offsetTop}px`);
      if (window.innerHeight - viewport.height > KEYBOARD_MIN_HEIGHT) root.setProperty("--safe-bottom", "0px");
      else root.removeProperty("--safe-bottom");
    }
    sync();
    viewport.addEventListener("resize", sync);
    viewport.addEventListener("scroll", sync);
    return () => {
      viewport.removeEventListener("resize", sync);
      viewport.removeEventListener("scroll", sync);
      clear();
    };
  }, []);
}
