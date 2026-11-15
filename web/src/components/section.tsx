import type { ReactNode } from "react";

import { Separator } from "./separator.tsx";

export const Section = ({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) => (
  <section className="mt-6">
    <h2 className="text-muted-foreground text-sm font-semibold tracking-wide uppercase">
      {title}
    </h2>
    <Separator className="mt-1.5 mb-3" />
    {children}
  </section>
);
