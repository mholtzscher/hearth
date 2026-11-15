import type * as React from "react";
import { cn } from "../lib/utils.ts";
import { fieldSizeStyles, fieldStyles, type ControlSize } from "./field.ts";

type NativeSelectProps = Omit<React.ComponentProps<"select">, "size"> & { size?: ControlSize };

function NativeSelect({ className, size = "default", ...props }: NativeSelectProps) {
  return (
    <select
      data-slot="native-select"
      data-size={size}
      className={cn(fieldStyles, fieldSizeStyles[size], className)}
      {...props}
    />
  );
}

export { NativeSelect, type NativeSelectProps };
