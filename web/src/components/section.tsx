import type { ReactNode } from "react";

export const Section = ({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) => (
  <section data-slot="section" className="mt-6">
    <h2 className="text-title mb-3 font-semibold">{title}</h2>
    {children}
  </section>
);
