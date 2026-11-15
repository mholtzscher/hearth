import { cn } from "../../lib/utils";

/** Classes for the preview Card: container queries and the conversation's 16px inset. */
export const artifactFrame = "@container p-4";
export const artifactHeader = "grid grid-cols-[36px_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2";
export const artifactIcon = "inline-grid size-9 place-items-center rounded-md bg-violet-soft text-violet";
export const artifactTitle = "text-body leading-[1.4] font-semibold [overflow-wrap:anywhere]";
export const artifactSummary = "text-label leading-normal text-muted-foreground";

export function artifactAction(open: boolean, inlineView = false) {
  return cn(
    "text-link",
    (!inlineView || open) && "@max-[440px]:col-[2/-1] @max-[440px]:justify-self-start",
    open && "border-transparent text-muted-foreground hover:bg-transparent hover:text-link @max-[440px]:-ms-3",
  );
}
