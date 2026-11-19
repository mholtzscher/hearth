import type { ComponentProps, ReactElement } from "react";
import { ResponsiveContainer } from "recharts";

import { cn } from "../lib/utils.ts";

/** Recharts sizing boundary following the shadcn chart container contract. */
export const ChartContainer = ({
  className,
  children,
  ...props
}: Omit<ComponentProps<"div">, "children"> & { children: ReactElement }) => (
  <div
    data-slot="chart"
    className={cn("text-caption flex aspect-video justify-center", className)}
    {...props}
  >
    <ResponsiveContainer width="100%" height="100%">
      {children}
    </ResponsiveContainer>
  </div>
);
