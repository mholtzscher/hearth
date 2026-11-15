import * as React from "react";

import { cn } from "../lib/utils.ts";
import { fieldSizeStyles, fieldStyles } from "./field.ts";
import type { ControlSize } from "./field.ts";

type InputProps = Omit<React.ComponentProps<"input">, "size"> & {
  size?: ControlSize;
};

const Input = ({ className, type, size = "default", ...props }: InputProps) => (
  <input
    type={type}
    data-slot="input"
    data-size={size}
    className={cn(
      fieldStyles,
      fieldSizeStyles[size],
      "file:text-label file:text-foreground file:inline-flex file:h-6 file:border-0 file:bg-transparent file:font-medium",
      className
    )}
    {...props}
  />
);

export { Input, type InputProps };
