import { useEffect, useLayoutEffect, useRef } from "react";
import type { RefObject } from "react";

/** Keep modal navigation inside its surface and restore the invoking control. */
export const useModalFocus = (
  container: RefObject<HTMLElement | null>,
  active: boolean,
  onClose: () => void,
  focusContainer = false,
  returnFocus?: RefObject<HTMLElement | null>
) => {
  const close = useRef(onClose);
  useLayoutEffect(() => {
    close.current = onClose;
  }, [onClose]);
  useEffect(() => {
    const element = container.current;
    if (!active || !element) {
      return;
    }
    const previous =
      returnFocus?.current ??
      (document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null);
    const siblings = new Map<HTMLElement, boolean>();
    // Inert siblings at every ancestor, without hiding the modal's own branch.
    for (
      let branch: HTMLElement | null = element;
      branch && branch !== document.body;
      branch = branch.parentElement
    ) {
      for (const sibling of branch.parentElement?.children ?? []) {
        if (sibling instanceof HTMLElement && sibling !== branch) {
          siblings.set(sibling, sibling.hasAttribute("inert"));
          sibling.toggleAttribute("inert", true);
        }
      }
    }
    const controls = () =>
      [
        ...element.querySelectorAll<HTMLElement>(
          'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex]:not([tabindex="-1"])'
        ),
      ].filter(
        (control) =>
          control.getClientRects().length > 0 && !control.closest("[inert]")
      );
    if (focusContainer) {
      element.focus();
    } else {
      (controls()[0] ?? element).focus();
    }
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        close.current();
        return;
      }
      if (event.key !== "Tab") {
        return;
      }
      const available = controls();
      const [first] = available;
      const last = available.at(-1);
      if (!(first && last)) {
        event.preventDefault();
        element?.focus();
        return;
      }
      if (
        event.shiftKey &&
        (document.activeElement === first || document.activeElement === element)
      ) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    element.addEventListener("keydown", keydown);
    return () => {
      element.removeEventListener("keydown", keydown);
      for (const [sibling, inert] of siblings) {
        sibling.toggleAttribute("inert", inert);
      }
      if (previous?.isConnected) {
        previous.focus();
      }
    };
  }, [active, container, focusContainer, returnFocus]);
};
