import type { ComponentProps } from "react";
import { cn } from "../../../lib/utils";

/** The label and field spacing shared by view and card editors. */
export function EditorLabel({ className, ...props }: ComponentProps<"label">) {
  return <label className={cn("my-2 grid gap-[5px]", className)} {...props} />;
}
