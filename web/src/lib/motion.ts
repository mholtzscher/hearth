export const prefersReducedMotion = () =>
  window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;

/** Read motion timing from the same CSS tokens as the animation. */
export const motionMilliseconds = (
  duration: "feedback" | "state" | "layout" | "travel"
) => {
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue(`--transition-duration-${duration}`)
    .trim();
  return value.endsWith("ms")
    ? Number(value.slice(0, -2))
    : Number(value.replace(/s$/u, "")) * 1000;
};
