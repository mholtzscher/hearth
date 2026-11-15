import type { ReactNode } from "react";
import { Separator } from "./separator.tsx";

export function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mt-6">
      <h2 className="text-sm font-semibold tracking-wide text-muted-foreground uppercase">
        {title}
      </h2>
      <Separator className="mt-1.5 mb-3" />
      {children}
    </section>
  );
}
