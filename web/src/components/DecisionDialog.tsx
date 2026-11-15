import { useState, type ReactNode } from "react";
import { Dialog as SharedDialog, DialogContent, DialogTitle } from "./ui/dialog";

/** Programmatic decisions restore focus to the control that opened them. */
export function DecisionDialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const [returnFocus] = useState(() => document.activeElement instanceof HTMLElement ? document.activeElement : null);
  return <SharedDialog open onOpenChange={open => { if (!open) onClose(); }}>
    <DialogContent aria-describedby={undefined} onPointerDownOutside={event => event.preventDefault()} onCloseAutoFocus={event => { event.preventDefault(); returnFocus?.focus(); }}>
      <DialogTitle>{title}</DialogTitle>
      <div className="[&>p]:my-[14px] [&>p]:mb-5 [&>p]:text-muted-foreground">{children}</div>
    </DialogContent>
  </SharedDialog>;
}
