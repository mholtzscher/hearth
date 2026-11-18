import { Label as LabelPrimitive } from "radix-ui";
import * as React from "react";

import { cn } from "../lib/utils.ts";

const Label = ({
  className,
  ...props
}: React.ComponentProps<typeof LabelPrimitive.Root>) => (
  <LabelPrimitive.Root
    data-slot="label"
    className={cn(
      "text-label flex items-center gap-2 leading-none font-medium select-none",
      className
    )}
    {...props}
  />
);

export { Label };
