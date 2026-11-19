import * as React from "react";

import { cn } from "../lib/utils.ts";

const Table = ({ className, ...props }: React.ComponentProps<"table">) => {
  const container = React.useRef<HTMLElement>(null);
  const table = React.useRef<HTMLTableElement>(null);
  React.useEffect(() => {
    const element = container.current;
    const content = table.current;
    if (!element || !content || typeof ResizeObserver === "undefined") {
      return;
    }
    // Focusability follows measured overflow, not the table's data or appearance.
    const measure = () => {
      element.tabIndex = element.scrollWidth > element.clientWidth ? 0 : -1;
    };
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    observer.observe(content);
    measure();
    return () => observer.disconnect();
  }, []);
  return (
    <section
      ref={container}
      data-slot="table-container"
      aria-label={props["aria-label"] ?? "Scrollable table"}
      className="relative w-full overflow-x-auto"
    >
      <table
        ref={table}
        data-slot="table"
        className={cn(
          "text-label w-full caption-bottom tabular-nums",
          className
        )}
        {...props}
      />
    </section>
  );
};

const TableHeader = ({
  className,
  ...props
}: React.ComponentProps<"thead">) => (
  <thead
    data-slot="table-header"
    className={cn("[&_tr]:border-b", className)}
    {...props}
  />
);

const TableBody = ({ className, ...props }: React.ComponentProps<"tbody">) => (
  <tbody
    data-slot="table-body"
    className={cn("[&_tr:last-child]:border-0", className)}
    {...props}
  />
);

const TableFooter = ({
  className,
  ...props
}: React.ComponentProps<"tfoot">) => (
  <tfoot
    data-slot="table-footer"
    className={cn(
      "bg-muted/50 border-t font-medium [&>tr]:last:border-b-0",
      className
    )}
    {...props}
  />
);

const TableRow = ({ className, ...props }: React.ComponentProps<"tr">) => (
  <tr
    data-slot="table-row"
    className={cn(
      "hover:bg-muted/50 has-aria-expanded:bg-muted/50 data-[state=selected]:bg-muted border-b transition-colors in-[thead]:hover:bg-transparent",
      className
    )}
    {...props}
  />
);

const TableHead = ({ className, ...props }: React.ComponentProps<"th">) => (
  <th
    data-slot="table-head"
    className={cn(
      "text-muted-foreground h-8 px-2 text-left align-middle font-medium whitespace-nowrap [&:has([role=checkbox])]:pr-0",
      className
    )}
    {...props}
  />
);

const TableCell = ({ className, ...props }: React.ComponentProps<"td">) => (
  <td
    data-slot="table-cell"
    className={cn(
      "px-2 py-1.5 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0",
      className
    )}
    {...props}
  />
);

const TableCaption = ({
  className,
  ...props
}: React.ComponentProps<"caption">) => (
  <caption
    data-slot="table-caption"
    className={cn("text-muted-foreground text-label mt-4", className)}
    {...props}
  />
);

export {
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
};
