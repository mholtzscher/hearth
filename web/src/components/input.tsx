import * as React from "react"
import { cn } from "../lib/utils.ts"
import { fieldSizeStyles, fieldStyles, type ControlSize } from "./field.ts"

type InputProps = Omit<React.ComponentProps<"input">, "size"> & { size?: ControlSize }

function Input({ className, type, size = "default", ...props }: InputProps) {
  return (
    <input
      type={type}
      data-slot="input"
      data-size={size}
      className={cn(
        fieldStyles,
        fieldSizeStyles[size],
        "file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-label file:font-medium file:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export { Input, type InputProps }
