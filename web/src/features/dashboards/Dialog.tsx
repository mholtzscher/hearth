import { useRef, type ReactNode } from "react";
import { useModalFocus } from "./useModalFocus";

/** Small modal for decisions that must not be bypassed by keyboard navigation. */
export function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const dialog = useRef<HTMLDivElement>(null);
  useModalFocus(dialog, true, onClose);
  return <div className="workspace-modal-backdrop"><div ref={dialog} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="workspace-dialog-title" className="workspace-modal"><h2 id="workspace-dialog-title">{title}</h2>{children}</div></div>;
}
