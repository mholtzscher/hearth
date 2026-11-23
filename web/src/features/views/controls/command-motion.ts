import { useState } from "react";

import type { ControlState } from "./queue";

/** A retained failure stays still. Only a newly rejected request shakes. */
export const useRefusal = (state?: ControlState): string | undefined => {
  const [previous, setPrevious] = useState({ state, rejected: false });
  if (previous.state !== state) {
    setPrevious({
      state,
      rejected: state?.phase === "failed" && state.failure.kind === "rejected",
    });
  }
  return previous.rejected ? "motion-safe:animate-refuse" : undefined;
};

/** Keep status words readable at the start of the fade. */
export const statusFade = "animate-fade [--motion-fade-from:0.8]";
