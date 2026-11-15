import { useState } from "react";
import type { ReactNode } from "react";

import { Dialog as SharedDialog, DialogContent, DialogTitle } from "./dialog";

/** Programmatic decisions restore focus to the control that opened them. */
export const DecisionDialog = ({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) => {
  const [returnFocus, setReturnFocus] = useState(() =>
    document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null
  );
  return (
    <SharedDialog
      open
      onOpenChange={(open) => {
        if (!open) {
          onClose();
        }
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        onPointerDownOutside={(event) => event.preventDefault()}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          returnFocus?.focus();
          setReturnFocus(null);
        }}
      >
        <DialogTitle>{title}</DialogTitle>
        {children}
      </DialogContent>
    </SharedDialog>
  );
};
