import type { Meta, StoryObj } from "@storybook/react-vite";
import type { ReactNode, RefObject } from "react";
import { expect } from "storybook/test";

import { expectNoHorizontalOverflow } from "../../.storybook/play";
import { Card } from "../components/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table";
import { Reading, ReadingUnit } from "../features/views/cards/reading";
import {
  colorToHex,
  ColorSwatch,
  rootVariable,
  SwatchGrid,
  TokenValue,
  useLiveStyle,
  variableColor,
} from "./live-style";
import {
  BRAND,
  BREAKPOINTS,
  CHART_SERIES,
  GRAY_RAMP,
  GRAY_USE,
  RADII,
  RADIUS_USE,
  ROLE_ALIASES,
  STATUS,
  TEXT_TOKENS,
  TYPE_USE,
} from "./tokens";
import type { Radius as RadiusName, TextToken } from "./tokens";

const meta = {
  title: "Design system/Foundations",
  tags: ["ai-generated", "!autodocs", "!manifest"],
} satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

/** Every displayed value matches the live custom property it names. */
const expectValuesMatchVariables = async (canvasElement: HTMLElement) => {
  const values = [
    ...canvasElement.querySelectorAll<HTMLElement>('[data-slot="token-value"]'),
  ];
  await expect(values.length).toBeGreaterThan(0);
  // Raw hex tokens must match exactly; a color-mix token shows its own expression.
  await Promise.all(
    values.map((value) =>
      expect(value, value.dataset.token).toHaveTextContent(
        rootVariable(value.dataset.token ?? "")
      )
    )
  );
};

const GrayRampSwatches = () => (
  <SwatchGrid>
    {GRAY_RAMP.map((token) => (
      <ColorSwatch key={token} variable={token} use={GRAY_USE[token]} />
    ))}
  </SwatchGrid>
);

export const GrayRamp: Story = {
  render: () => <GrayRampSwatches />,
  play: ({ canvasElement }) => expectValuesMatchVariables(canvasElement),
};

export const Brand: Story = {
  render: () => (
    <SwatchGrid>
      {BRAND.map(({ token, use }) => (
        <ColorSwatch key={token} variable={token} use={use} />
      ))}
    </SwatchGrid>
  ),
  play: ({ canvasElement }) => expectValuesMatchVariables(canvasElement),
};

export const Status: Story = {
  render: () => (
    <SwatchGrid>
      {STATUS.map(({ token, alias, use }) => (
        <ColorSwatch
          key={token}
          variable={token}
          detail={
            alias && (
              <span className="text-muted-foreground">
                Alias <code className="font-mono">{alias}</code>
              </span>
            )
          }
          use={use}
        />
      ))}
    </SwatchGrid>
  ),
  play: ({ canvasElement }) => expectValuesMatchVariables(canvasElement),
};

const AliasRow = ({ alias, token }: { alias: string; token: string }) => {
  const [aliasRef, aliasValue] = useLiveStyle<HTMLSpanElement>((element) =>
    variableColor(element, alias)
  );
  const [tokenRef, tokenValue] = useLiveStyle<HTMLSpanElement>((element) =>
    variableColor(element, token)
  );
  return (
    <TableRow data-alias={alias} data-token={token}>
      <TableCell>
        <span className="inline-flex items-center gap-2">
          <span
            ref={aliasRef}
            data-slot="alias-swatch"
            className="border-gray-4 size-4 shrink-0 rounded-[4px] border"
            style={{ backgroundColor: `var(--${alias})` }}
          />
          <code className="font-mono">{alias}</code>
        </span>
      </TableCell>
      <TableCell>
        <span
          ref={tokenRef}
          data-slot="token-swatch"
          className="hidden"
          style={{ backgroundColor: `var(--${token})` }}
        />
        <span className="flex flex-wrap gap-x-3">
          <code className="font-mono">{token}</code>
          <code
            data-slot="alias-value"
            data-matches={aliasValue === tokenValue}
            className="text-gray-8 font-mono"
          >
            {aliasValue ?? "…"}
          </code>
        </span>
      </TableCell>
    </TableRow>
  );
};

export const RoleAliases: Story = {
  render: () => (
    <Table aria-label="Role aliases">
      <TableHeader>
        <TableRow>
          <TableHead>Alias</TableHead>
          <TableHead>Token and value</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {ROLE_ALIASES.map(({ alias, token }) => (
          <AliasRow key={alias} alias={alias} token={token} />
        ))}
      </TableBody>
    </Table>
  ),
  play: async ({ canvasElement }) => {
    const values = canvasElement.querySelectorAll<HTMLElement>(
      '[data-slot="alias-value"]'
    );
    await expect(values).toHaveLength(ROLE_ALIASES.length);
    // Each alias resolves to the same color as the token it names.
    await Promise.all(
      [...values].map((value) =>
        expect(value, value.closest("tr")?.dataset.alias).toHaveAttribute(
          "data-matches",
          "true"
        )
      )
    );
  },
};

export const ChartSeries: Story = {
  render: () => (
    <SwatchGrid>
      {CHART_SERIES.map(({ variable, name, token }, index) => (
        <ColorSwatch
          key={variable}
          variable={variable}
          name={`${index + 1} · ${name}`}
          detail={
            <code className="text-muted-foreground font-mono">
              --{variable} · {token}
            </code>
          }
        />
      ))}
    </SwatchGrid>
  ),
  play: ({ canvasElement }) => expectValuesMatchVariables(canvasElement),
};

/** Each sample sets size with its token and applies DESIGN.md's weight, line height, and tracking. */
const TYPE_SAMPLES: Record<
  TextToken,
  (ref: RefObject<HTMLSpanElement | null>) => ReactNode
> = {
  reading: (ref) => (
    <Reading as="span" className="block">
      <span ref={ref}>21.5</span>
      <ReadingUnit>°C</ReadingUnit>
    </Reading>
  ),
  "reading-narrow": (ref) => (
    <Reading as="span" className="text-reading-narrow block">
      <span ref={ref}>94.0</span>
      <ReadingUnit>%</ReadingUnit>
    </Reading>
  ),
  headline: (ref) => (
    <span
      ref={ref}
      className="text-headline block leading-[1.3] font-medium tracking-[-.03em]"
    >
      Devices
    </span>
  ),
  "headline-narrow": (ref) => (
    <span
      ref={ref}
      className="text-headline-narrow block leading-[1.3] font-medium tracking-[-.03em]"
    >
      Devices
    </span>
  ),
  title: (ref) => (
    <span ref={ref} className="text-title block font-semibold">
      Living room
    </span>
  ),
  body: (ref) => (
    <span ref={ref} className="text-body block leading-[1.7]">
      The living room is 21.5 °C, half a degree warmer than an hour ago.
    </span>
  ),
  label: (ref) => (
    <span ref={ref} className="text-label block leading-normal">
      Observed 2m ago
    </span>
  ),
  "compact-reading": (ref) => (
    <span
      ref={ref}
      className="text-compact-reading block leading-[1.3] tracking-[-.02em] tabular-nums"
    >
      21.5
    </span>
  ),
  caption: (ref) => (
    <span ref={ref} className="text-caption block">
      Previous 7 days
    </span>
  ),
};

const TypeRow = ({ token }: { token: TextToken }) => {
  const [ref, value] = useLiveStyle<HTMLSpanElement>(
    (_element, style) =>
      `${style.fontSize} / ${style.lineHeight} · ${style.fontWeight}`
  );
  return (
    <div
      data-token={token}
      className="border-border tablet:grid-cols-[220px_minmax(0,1fr)] grid gap-x-6 gap-y-2 border-t py-4 first:border-t-0 first:pt-0"
    >
      <div className="grid content-start gap-0.5">
        <code className="text-label text-foreground font-mono">
          text-{token}
        </code>
        <TokenValue value={value} token={`text-${token}`} />
        <span className="text-label text-muted-foreground leading-normal">
          {TYPE_USE[token]}
        </span>
      </div>
      <div className="min-w-0 [overflow-wrap:anywhere]">
        {TYPE_SAMPLES[token](ref)}
      </div>
    </div>
  );
};

const TypeScaleRows = () => (
  <div className="grid">
    {TEXT_TOKENS.map((token) => (
      <TypeRow key={token} token={token} />
    ))}
  </div>
);

export const TypeScale: Story = {
  render: () => <TypeScaleRows />,
  play: async ({ canvasElement }) => {
    // The readout comes from the sample, so it stays empty if a sample is missing.
    await Promise.all(
      [
        ...canvasElement.querySelectorAll<HTMLElement>(
          '[data-slot="token-value"]'
        ),
      ].map((value) => expect(value).not.toHaveTextContent("…"))
    );
  },
};

const RADIUS_CLASSES: Record<RadiusName, string> = {
  md: "rounded-md",
  card: "rounded-card",
};

const RadiusSample = ({ radius }: { radius: RadiusName }) => {
  const [ref, value] = useLiveStyle<HTMLDivElement>(
    (_element, style) => style.borderTopLeftRadius
  );
  return (
    <figure className="m-0 grid content-start gap-2">
      <div
        ref={ref}
        className={`border-gray-6 bg-gray-2 h-16 border ${RADIUS_CLASSES[radius]}`}
      />
      <figcaption className="text-label grid gap-0.5 leading-normal">
        <code className="text-foreground font-mono">rounded-{radius}</code>
        <TokenValue value={value} token={`radius-${radius}`} />
        <span className="text-muted-foreground">{RADIUS_USE[radius]}</span>
      </figcaption>
    </figure>
  );
};

export const Radius: Story = {
  render: () => (
    <SwatchGrid>
      {RADII.map((radius) => (
        <RadiusSample key={radius} radius={radius} />
      ))}
    </SwatchGrid>
  ),
};

/** Drops Tailwind's empty ring layers and writes colors as hex. */
const shadowLayers = (boxShadow: string): string => {
  const layers = boxShadow
    .split(/,(?![^(]*\))/u)
    .map((layer) => layer.trim())
    .filter(
      (layer) => layer !== "none" && !layer.startsWith("rgba(0, 0, 0, 0)")
    )
    .map((layer) =>
      layer.replace(/rgba?\([^)]*\)/u, (color) => colorToHex(color) ?? color)
    );
  return layers.length > 0 ? layers.join(", ") : "none";
};

const ShadowSample = ({
  name,
  use,
  className,
}: {
  name: string;
  use: string;
  className: string;
}) => {
  const [ref, value] = useLiveStyle<HTMLDivElement>((_element, style) =>
    shadowLayers(style.boxShadow)
  );
  return (
    <figure className="m-0 grid min-w-0 content-start gap-3">
      <Card ref={ref} className={`h-24 ${className}`} />
      <figcaption className="text-label grid gap-0.5 leading-normal">
        <span className="text-foreground font-medium">{name}</span>
        <TokenValue value={value} token={`shadow-${name}`} />
        <span className="text-muted-foreground">{use}</span>
      </figcaption>
    </figure>
  );
};

export const Elevation: Story = {
  render: () => (
    <div className="bg-gray-3 grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-6 rounded-md p-6">
      <ShadowSample
        name="Flat"
        use="Cards and every surface at rest"
        className=""
      />
      <ShadowSample
        name="Overlay"
        use="Dialog and popovers"
        className="border-gray-4 bg-popover shadow-[0_18px_60px_#0008]"
      />
      <ShadowSample
        name="Drawer"
        use="Navigation and Recent drawers"
        className="shadow-[12px_0_40px_#0006]"
      />
    </div>
  ),
  play: async ({ canvasElement }) => {
    const flat = canvasElement.querySelector('[data-token="shadow-Flat"]');
    await expect(flat).toHaveTextContent("none");
  },
};

/** The media condition Tailwind compiled for a breakpoint variant, read from the live stylesheets. */
const breakpointCondition = (name: string): string | undefined => {
  const prefix = `.${name}\\:`;
  const visit = (
    rules: CSSRuleList,
    condition: string | undefined
  ): string | undefined => {
    for (const rule of rules) {
      const within =
        rule instanceof CSSMediaRule ? rule.conditionText : condition;
      if (
        within &&
        rule instanceof CSSStyleRule &&
        rule.selectorText.startsWith(prefix)
      ) {
        return within;
      }
      // Nested rules put the media query inside the utility instead.
      if (
        rule instanceof CSSStyleRule &&
        rule.selectorText.startsWith(prefix)
      ) {
        const media = [...rule.cssRules].find(
          (child): child is CSSMediaRule => child instanceof CSSMediaRule
        );
        if (media) {
          return media.conditionText;
        }
      }
      if (rule instanceof CSSGroupingRule) {
        const found = visit(rule.cssRules, within);
        if (found) {
          return found;
        }
      }
    }
    return undefined;
  };
  for (const sheet of document.styleSheets) {
    try {
      const found = visit(sheet.cssRules, undefined);
      if (found) {
        return found;
      }
    } catch {
      // A cross-origin sheet hides its rules; Hearth's CSS is same-origin.
    }
  }
  return undefined;
};

/** `(width >= 45.0625rem)` reads as `721px`. */
const conditionPixels = (condition: string): string => {
  const rem = /(?<value>[\d.]+)rem/u.exec(condition)?.groups?.value;
  // Computed font sizes are always in px.
  const root = Number(
    getComputedStyle(document.documentElement).fontSize.slice(0, -2)
  );
  return rem ? `${Number(rem) * root}px` : condition;
};

const BreakpointRow = ({
  variable,
  name,
  use,
}: (typeof BREAKPOINTS)[number]) => {
  const [ref, value] = useLiveStyle<HTMLTableRowElement>(() => {
    const condition = breakpointCondition(name);
    return condition ? `${conditionPixels(condition)}\n${condition}` : "—";
  });
  return (
    <TableRow ref={ref}>
      <TableCell>
        <code className="font-mono">{name}:</code>
      </TableCell>
      <TableCell>
        <TokenValue
          value={value}
          token={variable}
          className="text-label whitespace-pre-line"
        />
      </TableCell>
      <TableCell>{use}</TableCell>
    </TableRow>
  );
};

const LAYOUT_FACTS: [string, string][] = [
  ["Sidebar", "clamp(218px, 22vw, 340px); a 104px rail at 721–980px"],
  ["Conversation beside a canvas", "clamp(368px, 28vw, 480px)"],
  ["Canvas content", "1080px beside the conversation, 1440px alone"],
  ["Pages", "1440px maximum, 28px gutters, 20px on phones"],
  ["Grid minimum", "248px; 160px for tile and Metric or State grids"],
  ["Grid gap", "12px; stacks use 8, 16, or 24px"],
];

export const Layout: Story = {
  render: () => (
    <div className="grid gap-6">
      <Table aria-label="Breakpoints">
        <TableHeader>
          <TableRow>
            <TableHead>Variant</TableHead>
            <TableHead>Starts at</TableHead>
            <TableHead>Used for</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {BREAKPOINTS.map((breakpoint) => (
            <BreakpointRow key={breakpoint.name} {...breakpoint} />
          ))}
        </TableBody>
      </Table>
      <dl className="text-label tablet:grid-cols-[minmax(0,240px)_minmax(0,1fr)] desktop:gap-x-10 grid gap-x-6 gap-y-2 leading-normal">
        {LAYOUT_FACTS.map(([term, detail]) => (
          <div key={term} className="contents">
            <dt className="text-muted-foreground">{term}</dt>
            <dd className="text-foreground m-0">{detail}</dd>
          </div>
        ))}
      </dl>
    </div>
  ),
  play: async ({ canvasElement }) => {
    await expect(
      canvasElement.querySelector('[data-token="breakpoint-tablet"]')
    ).toHaveTextContent("721px");
    await expect(
      canvasElement.querySelector('[data-token="breakpoint-desktop"]')
    ).toHaveTextContent("981px");
  },
};

/** The whole page fits a phone. */
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  render: () => (
    <div className="grid gap-8">
      <GrayRampSwatches />
      <TypeScaleRows />
    </div>
  ),
  play: ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
