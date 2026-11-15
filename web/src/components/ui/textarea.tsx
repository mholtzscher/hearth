import * as React from "react"
import { cn } from "../../lib/utils.ts"
import { fieldStyles } from "./field.ts"

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        fieldStyles,
        "min-h-11 py-2",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
