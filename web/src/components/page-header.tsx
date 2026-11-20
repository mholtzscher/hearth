import type { ReactNode } from "react";

/** The product heading shared by pages and canvas titles. */
export const pageHeadingClass =
  "text-headline text-balance [overflow-wrap:anywhere]";

/** A page title and its actions, with optional context below. */
export const PageHeader = ({
  title,
  description,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
}) => (
  <header data-slot="page-header" className="mb-5 border-b pb-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h1 data-slot="page-heading" className={pageHeadingClass}>
        {title}
      </h1>
      {children && (
        <div
          data-slot="page-actions"
          className="flex max-w-full min-w-0 flex-wrap items-center gap-2"
        >
          {children}
        </div>
      )}
    </div>
    {description && (
      <div
        data-slot="page-description"
        className="text-label text-muted-foreground mt-2"
      >
        {description}
      </div>
    )}
  </header>
);
