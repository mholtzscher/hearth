import { ResponsiveContainer } from "recharts";
import type { ComponentProps, ReactElement } from "react";
import { cn } from "cn";

/** Recharts sizing boundary following the shadcn chart container contract. */
export function ChartContainer({ className, children, ...props }: Omit<ComponentProps<"div">, "children"> & { children: ReactElement }) {
  return <div data-slot="chart" className={cn("flex aspect-video justify-center text-xs", className)} {...props}>
    <ResponsiveContainer width="100%" height="100%">{children}</ResponsiveContainer>
  </div>;
}
