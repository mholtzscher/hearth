import { useLayoutEffect, useRef, useState } from "react";
import type { CSSProperties, ReactNode } from "react";

import { cn } from "../lib/utils";

const byte = (value: number) => Math.round(value).toString(16).padStart(2, "0");

/** `rgb(31, 31, 31)` becomes `#1f1f1f`; a translucent color keeps its alpha byte. */
export const colorToHex = (color: string): string | undefined => {
  const match =
    /^rgba?\(\s*(?<r>[\d.]+)[,\s]+(?<g>[\d.]+)[,\s]+(?<b>[\d.]+)(?:\s*[,/]\s*(?<a>[\d.]+%?))?\s*\)$/u.exec(
      color.trim()
    );
  if (!match?.groups) {
    return undefined;
  }
  const { r, g, b, a } = match.groups;
  const hex = `#${byte(Number(r))}${byte(Number(g))}${byte(Number(b))}`;
  if (a === undefined) {
    return hex;
  }
  const alpha = a.endsWith("%") ? Number(a.slice(0, -1)) / 100 : Number(a);
  return alpha === 1 ? hex : `${hex}${byte(alpha * 255)}`;
};

/** The live value of a custom property on the document root, such as `--gray-3`. */
export const rootVariable = (name: string): string =>
  getComputedStyle(document.documentElement)
    .getPropertyValue(`--${name}`)
    .trim();

/** A custom property's resolved color, as hex where the browser reports sRGB. */
export const variableColor = (element: HTMLElement, variable: string) =>
  colorToHex(getComputedStyle(element).backgroundColor) ??
  rootVariable(variable);

/**
 * Renders the value `read` takes from the element that `children` receives,
 * after layout, so captions show the live CSS rather than a typed copy.
 */
export const useLiveStyle = <T extends HTMLElement>(
  read: (element: T, style: CSSStyleDeclaration) => string
) => {
  const ref = useRef<T>(null);
  const reader = useRef(read);
  const [value, setValue] = useState<string>();
  useLayoutEffect(() => {
    if (ref.current) {
      setValue(reader.current(ref.current, getComputedStyle(ref.current)));
    }
  }, []);
  return [ref, value] as const;
};

/** A token value as the page reads it; tests find it by `data-slot`. */
export const TokenValue = ({
  value,
  token,
  className,
}: {
  value: string | undefined;
  token: string;
  className?: string;
}) => (
  <code
    data-slot="token-value"
    data-token={token}
    className={cn(
      "text-caption text-gray-8 font-mono leading-normal",
      className
    )}
  >
    {value ?? "…"}
  </code>
);

export const ColorSwatch = ({
  variable,
  name,
  detail,
  use,
}: {
  /** The custom property without its leading dashes, such as `gray-3`. */
  variable: string;
  name?: ReactNode;
  detail?: ReactNode;
  use?: string;
}) => {
  const [ref, value] = useLiveStyle<HTMLDivElement>((element) =>
    variableColor(element, variable)
  );
  const style: CSSProperties = { backgroundColor: `var(--${variable})` };
  return (
    <figure className="m-0 grid min-w-0 content-start gap-2">
      <div
        ref={ref}
        data-slot="swatch"
        className="border-gray-4 h-14 rounded-md border"
        style={style}
      />
      <figcaption className="text-label grid min-w-0 gap-0.5 leading-normal">
        <span className="text-foreground font-medium [overflow-wrap:anywhere]">
          {name ?? variable}
        </span>
        {detail}
        <TokenValue value={value} token={variable} />
        {use && <span className="text-muted-foreground">{use}</span>}
      </figcaption>
    </figure>
  );
};

/** A responsive grid of swatches or samples. */
export const SwatchGrid = ({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) => (
  <div
    className={cn(
      "grid grid-cols-[repeat(auto-fill,minmax(132px,1fr))] gap-x-3 gap-y-5",
      className
    )}
  >
    {children}
  </div>
);
