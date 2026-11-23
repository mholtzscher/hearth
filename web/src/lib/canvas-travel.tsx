import { addTransitionType, startTransition, ViewTransition } from "react";
import type { ReactNode } from "react";

/** Travel only follows explicit moves of a view between conversation and canvas. */
export const canTravel = () =>
  typeof document.startViewTransition === "function" &&
  window.matchMedia?.(
    "(min-width: 721px) and (prefers-reduced-motion: no-preference)"
  ).matches === true;

export const runTravel = (action: () => void, opening = false) => {
  if (!canTravel()) {
    action();
    return;
  }
  startTransition(() => {
    addTransitionType("hearth-travel");
    if (opening) {
      addTransitionType("hearth-travel-open");
    }
    action();
  });
};

/** Move the view as one snapshot, so cards cannot overtake each other. */
export const TravelingView = ({
  artifactId,
  children,
}: {
  artifactId?: string | null;
  children: ReactNode;
}) =>
  artifactId && canTravel() ? (
    <ViewTransition
      name={`view-${CSS.escape(artifactId)}`}
      default="none"
      share={{ default: "none", "hearth-travel": "hearth-travel" }}
    >
      {children}
    </ViewTransition>
  ) : (
    children
  );
