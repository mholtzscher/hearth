// @vitest-environment node
import { readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

/**
 * DESIGN.md's front matter and index.css must name the same token values.
 * Change both together; this test says which side drifted. The palette must
 * also meet DESIGN.md's Color Math Rule, measured in OKLab and WCAG contrast.
 */

const web = path.resolve(import.meta.dirname, "..");
const designMd = readFileSync(path.join(web, "../DESIGN.md"), "utf-8");
const css = readFileSync(path.join(web, "src/index.css"), "utf-8").replaceAll(
  /\/\*[\s\S]*?\*\//gu,
  ""
);

interface Tree {
  [key: string]: Tree | string;
}

/**
 * Parses DESIGN.md's front matter: nested two-space-indented maps of scalar
 * `key: value` pairs, with optional double quotes. That is the whole subset
 * the format uses.
 */
const parseFrontMatter = (markdown: string): Tree => {
  const match = /^---\n(?<body>[\s\S]*?)\n---\n/u.exec(markdown);
  if (!match?.groups) {
    throw new Error("DESIGN.md has no front matter.");
  }
  const root: Tree = {};
  const stack: { indent: number; node: Tree }[] = [{ indent: -1, node: root }];
  for (const line of match.groups.body.split("\n")) {
    if (line.trim() === "" || line.trim().startsWith("#")) {
      continue;
    }
    const entry = /^(?<indent> *)(?<key>[\w-]+):\s*(?<value>.*)$/u.exec(line);
    if (!entry?.groups) {
      throw new Error(`Unsupported front matter line: ${line}`);
    }
    const indent = entry.groups.indent.length;
    while ((stack.at(-1)?.indent ?? -1) >= indent) {
      stack.pop();
    }
    const parent = stack.at(-1)?.node ?? root;
    const value = entry.groups.value.trim();
    if (value === "") {
      const child: Tree = {};
      parent[entry.groups.key] = child;
      stack.push({ indent, node: child });
    } else {
      parent[entry.groups.key] = value.replace(/^"(?<inner>.*)"$/u, "$<inner>");
    }
  }
  return root;
};

const section = (tree: Tree, name: string): Tree => {
  const value = tree[name];
  if (typeof value !== "object") {
    throw new TypeError(`DESIGN.md front matter has no ${name} section.`);
  }
  return value;
};

/** Custom property declarations inside the first block whose prelude matches. */
const declarations = (prelude: RegExp): Map<string, string> => {
  const start = prelude.exec(css);
  if (!start) {
    throw new Error(`index.css has no ${prelude} block.`);
  }
  const body = css.slice(start.index + start[0].length);
  const block = body.slice(0, body.indexOf("\n}"));
  return new Map(
    [...block.matchAll(/(?<name>--[\w-]+)\s*:\s*(?<value>[^;]+);/gu)].map(
      (found) => [found[1], found[2].trim()]
    )
  );
};

const root = declarations(/:root\s*\{/u);
const theme = declarations(/@theme inline\s*\{/u);

/** Replaces every `var(--name)` with that custom property's own resolved value. */
const resolve = (value: string, seen: string[] = []): string =>
  value.replaceAll(/var\((?<name>--[\w-]+)\)/gu, (_, name: string) => {
    const next = root.get(name) ?? theme.get(name);
    if (next === undefined || seen.includes(name)) {
      throw new Error(`index.css cannot resolve ${name}.`);
    }
    return resolve(next, [...seen, name]);
  });

/** Resolves a length to pixels, at 16px per rem, including `calc(<length> * k)`. */
const pixels = (value: string): number => {
  const resolved = resolve(value).trim();
  const product =
    /^calc\((?<length>[\d.]+(?:px|rem))\s*\*\s*(?<factor>[\d.]+)\)$/u.exec(
      resolved
    );
  if (product?.groups) {
    return pixels(product.groups.length) * Number(product.groups.factor);
  }
  const length = /^(?<amount>[\d.]+)(?<unit>px|rem)$/u.exec(resolved);
  if (!length?.groups) {
    throw new Error(`Unsupported length: ${value}`);
  }
  const amount = Number(length.groups.amount);
  return length.groups.unit === "rem" ? amount * 16 : amount;
};

const design = parseFrontMatter(designMd);

/** DESIGN.md typography keys and the index.css companion each one documents. */
const TYPE_COMPANIONS = [
  ["lineHeight", "line-height"],
  ["letterSpacing", "letter-spacing"],
  ["fontWeight", "font-weight"],
] as const;

/** DESIGN.md color names whose custom property is not `--<name>`. */
const COLOR_VARIABLES: Record<string, string> = {
  "chart-orchid": "--chart-3",
  "chart-pale-gold": "--chart-5",
  "chart-ice": "--chart-7",
  "chart-sand": "--chart-8",
  primary: "--primary",
};

/** `#rrggbbaa` as its base color and alpha percentage. */
const translucent = (hex: string) => {
  const match = /^#(?<base>[\da-f]{6})(?<alpha>[\da-f]{2})$/iu.exec(hex);
  return match?.groups
    ? {
        base: `#${match.groups.base}`.toLowerCase(),
        alpha: (Number.parseInt(match.groups.alpha, 16) / 255) * 100,
      }
    : undefined;
};

const colorVariable = (name: string) => COLOR_VARIABLES[name] ?? `--${name}`;
const declaredColor = (name: string): string => {
  const declared = root.get(colorVariable(name));
  if (declared === undefined) {
    throw new Error(
      `DESIGN.md color ${name} has no ${colorVariable(name)} in index.css :root.`
    );
  }
  return declared;
};
const colors = Object.entries(section(design, "colors")) as [string, string][];
const opaqueColors = colors.filter(([, value]) => !translucent(value));
const translucentColors = colors.filter(([, value]) => translucent(value));

describe("DESIGN.md front matter parser", () => {
  it("reads nested maps and unquotes scalars", () => {
    expect(
      parseFrontMatter(
        '---\nname: Hearth\ncolors:\n  gray-0: "#000000"\ntypography:\n  body:\n    fontSize: "16px"\n    lineHeight: 1.7\n---\n'
      )
    ).toStrictEqual({
      name: "Hearth",
      colors: { "gray-0": "#000000" },
      typography: { body: { fontSize: "16px", lineHeight: "1.7" } },
    });
  });
});

describe("DESIGN.md matches index.css", () => {
  it.each(opaqueColors)("color %s", (name, documented) => {
    const variable = colorVariable(name);
    const value = resolve(declaredColor(name)).toLowerCase();
    expect(
      value,
      `DESIGN.md color ${name} is ${documented}; index.css ${variable} resolves to ${value}.`
    ).toBe(documented.toLowerCase());
  });

  // DESIGN.md writes a translucent color as #rrggbbaa; index.css mixes it with transparent.
  it.each(translucentColors)("color %s", (name, documented) => {
    const variable = colorVariable(name);
    const value = resolve(declaredColor(name)).toLowerCase();
    const expected = translucent(documented);
    const mix =
      /^color-mix\(in oklch, (?<base>#[\da-f]{6}) (?<percent>[\d.]+)%, transparent\)$/u.exec(
        value
      )?.groups;
    expect(
      mix,
      `index.css ${variable} is ${value}, not a color mixed with transparent.`
    ).toBeDefined();
    expect(mix?.base, `${name} base color`).toBe(expected?.base);
    expect(
      Math.abs(Number(mix?.percent) - (expected?.alpha ?? Number.NaN)),
      `DESIGN.md ${name} alpha is ${expected?.alpha.toFixed(1)}%; index.css uses ${mix?.percent}%.`
    ).toBeLessThan(0.5);
  });

  it.each(Object.entries(section(design, "typography")))(
    "type role %s",
    (name, style) => {
      const declared = theme.get(`--text-${name}`);
      expect(declared, `index.css @theme has no --text-${name}.`).toBeDefined();
      const documented = (style as Tree).fontSize as string;
      expect(
        `${pixels(declared as string)}px`,
        `DESIGN.md ${name} fontSize is ${documented}; index.css --text-${name} is ${declared}.`
      ).toBe(documented);
      // Each role's companions in index.css match DESIGN.md, including absent ones.
      for (const [key, suffix] of TYPE_COMPANIONS) {
        const companion = `--text-${name}--${suffix}`;
        expect(
          theme.get(companion),
          `DESIGN.md ${name} ${key} must match index.css ${companion}.`
        ).toBe((style as Tree)[key]);
      }
    }
  );

  it.each(Object.entries(section(design, "rounded")))(
    "radius %s",
    (name, documented) => {
      const declared = theme.get(`--radius-${name}`);
      expect(
        declared,
        `index.css @theme has no --radius-${name}.`
      ).toBeDefined();
      expect(
        `${pixels(declared as string)}px`,
        `DESIGN.md rounded ${name} is ${documented}; index.css --radius-${name} is ${declared}.`
      ).toBe(documented);
    }
  );

  it("documents every text size in index.css", () => {
    const declared = [...theme.keys()]
      .filter((name) => name.startsWith("--text-"))
      .map((name) => name.slice("--text-".length))
      .filter((name) => !name.includes("--"));
    expect(
      declared.filter((name) => !(name in section(design, "typography"))),
      "Add these index.css text sizes to DESIGN.md typography."
    ).toStrictEqual([]);
  });

  it("documents every radius in index.css", () => {
    const declared = [...theme.keys()]
      .filter((name) => name.startsWith("--radius-"))
      .map((name) => name.slice("--radius-".length));
    expect(
      declared.filter((name) => !(name in section(design, "rounded"))),
      "Add these index.css radii to DESIGN.md rounded."
    ).toStrictEqual([]);
  });
});

const byte = (pair: string) => Number.parseInt(pair, 16) / 255;

/** `#rrggbb` as sRGB channels from 0 to 1. */
const channels = (hex: string): [number, number, number] => {
  const match = /^#(?<r>[\da-f]{2})(?<g>[\da-f]{2})(?<b>[\da-f]{2})$/iu.exec(
    hex
  );
  if (!match?.groups) {
    throw new Error(`Not an opaque #rrggbb color: ${hex}`);
  }
  const { r, g, b } = match.groups;
  return [byte(r), byte(g), byte(b)];
};

const linear = (channel: number) =>
  channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;

/** Björn Ottosson's reference sRGB to OKLab conversion. */
const oklab = (hex: string): [number, number, number] => {
  const [r, g, b] = channels(hex).map(linear);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ];
};

/** OKLCH lightness and chroma. */
const oklch = (hex: string) => {
  const [lightness, a, b] = oklab(hex);
  return { lightness, chroma: Math.hypot(a, b) };
};

/** Euclidean distance in OKLab: how different two colors look. */
const distance = (first: string, second: string) => {
  const [l1, a1, b1] = oklab(first);
  const [l2, a2, b2] = oklab(second);
  return Math.hypot(l1 - l2, a1 - a2, b1 - b2);
};

/** WCAG 2 relative luminance. */
const luminance = (hex: string) => {
  const [r, g, b] = channels(hex).map(linear);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};

/** WCAG 2 contrast ratio between two opaque colors. */
const contrast = (first: string, second: string) => {
  const [x, y] = [luminance(first), luminance(second)];
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
};

/** `color` at `alpha` over an opaque `background`, composited in sRGB as browsers do. */
const over = (color: string, alpha: number, background: string) => {
  const top = channels(color);
  const bottom = channels(background);
  return `#${top
    .map((channel, index) =>
      Math.round((channel * alpha + bottom[index] * (1 - alpha)) * 255)
        .toString(16)
        .padStart(2, "0")
    )
    .join("")}`;
};

/** DESIGN.md color tokens for chart series 1 to 8, in order. */
const CHART_SERIES = [
  "gray-10",
  "violet",
  "chart-orchid",
  "info",
  "chart-pale-gold",
  "gray-7",
  "chart-ice",
  "chart-sand",
];
const CHART_ONLY = [
  "chart-orchid",
  "chart-pale-gold",
  "chart-ice",
  "chart-sand",
];
const STATUS = ["ok", "warn", "error", "info"];
const GRAY_RAMP = Array.from({ length: 11 }, (_, step) => `gray-${step}`);
/** Text colors that sit on surfaces no lighter than gray-3. */
const SURFACE_TEXT = [
  "gray-8",
  "gray-10",
  "violet-text",
  "chart-orchid",
  "chart-pale-gold",
  "chart-ice",
];
/** Dimmed readings and history headings use gray-7 text only on these surfaces. */
const QUIET_TEXT_SURFACES = ["gray-0", "gray-1"];
/** Status chips, badges, and destructive buttons tint their surface with their own color. */
const STATUS_TINTS = [0.1, 0.15, 0.2];
const TINTED_SURFACES = ["gray-1", "gray-2"];

/** DESIGN.md's Color Math Rule limits. */
const LIMITS = {
  textContrast: 4.5,
  statusLightness: [0.68, 0.82],
  statusChroma: 0.11,
  brandDistance: 0.11,
  chartChroma: [0.075, 0.12],
  seriesDistance: 0.12,
} as const;

type Palette = Record<string, string>;

const pick = (palette: Palette, name: string) => {
  const value = palette[name];
  if (value === undefined) {
    throw new Error(`The palette has no ${name} color.`);
  }
  return value;
};

const pairs = <T>(items: readonly T[]): [T, T][] =>
  items.flatMap((first, index) =>
    items.slice(index + 1).map((second): [T, T] => [first, second])
  );

/** Each rule returns one message per violation; an empty list passes. */
const PALETTE_RULES: [string, (palette: Palette) => string[]][] = [
  [
    "the gray ramp is untinted and lightens at every step",
    (palette) =>
      GRAY_RAMP.flatMap((name, step) => {
        const [r, g, b] = channels(pick(palette, name));
        const tinted = r !== g || g !== b ? [`${name} is tinted.`] : [];
        const previous = GRAY_RAMP[step - 1];
        const darker =
          previous !== undefined &&
          oklch(pick(palette, name)).lightness <=
            oklch(pick(palette, previous)).lightness
            ? [`${name} is not lighter than ${previous}.`]
            : [];
        return [...tinted, ...darker];
      }),
  ],
  [
    "surface text is readable on gray-3",
    (palette) =>
      SURFACE_TEXT.flatMap((name) => {
        const ratio = contrast(pick(palette, name), pick(palette, "gray-3"));
        return ratio < LIMITS.textContrast
          ? [`${name} on gray-3 is ${ratio.toFixed(2)}:1.`]
          : [];
      }),
  ],
  [
    "gray-7 text is readable on gray-0 and gray-1",
    (palette) =>
      QUIET_TEXT_SURFACES.flatMap((surface) => {
        const ratio = contrast(pick(palette, "gray-7"), pick(palette, surface));
        return ratio < LIMITS.textContrast
          ? [`gray-7 on ${surface} is ${ratio.toFixed(2)}:1.`]
          : [];
      }),
  ],
  [
    "status text is readable on its own tint",
    (palette) =>
      STATUS.flatMap((name) =>
        STATUS_TINTS.flatMap((alpha) =>
          TINTED_SURFACES.flatMap((surface) => {
            const color = pick(palette, name);
            const ratio = contrast(
              color,
              over(color, alpha, pick(palette, surface))
            );
            return ratio < LIMITS.textContrast
              ? [
                  `${name} on its ${alpha * 100}% tint over ${surface} is ${ratio.toFixed(2)}:1.`,
                ]
              : [];
          })
        )
      ),
  ],
  [
    "on-orange is readable on orange and orange-hover",
    (palette) =>
      ["orange", "orange-hover"].flatMap((name) => {
        const ratio = contrast(pick(palette, "on-orange"), pick(palette, name));
        return ratio < LIMITS.textContrast
          ? [`on-orange on ${name} is ${ratio.toFixed(2)}:1.`]
          : [];
      }),
  ],
  [
    "status colors share a lightness and chroma band",
    (palette) =>
      STATUS.flatMap((name) => {
        const { lightness, chroma } = oklch(pick(palette, name));
        const [low, high] = LIMITS.statusLightness;
        return lightness < low ||
          lightness > high ||
          chroma < LIMITS.statusChroma
          ? [
              `${name} has L ${lightness.toFixed(3)} and C ${chroma.toFixed(3)}.`,
            ]
          : [];
      }),
  ],
  [
    "status colors stay clear of orange and violet",
    (palette) =>
      STATUS.flatMap((name) =>
        ["orange", "violet"].flatMap((brand) => {
          const gap = distance(pick(palette, name), pick(palette, brand));
          return gap < LIMITS.brandDistance
            ? [`${name} is ${gap.toFixed(3)} from ${brand}.`]
            : [];
        })
      ),
  ],
  [
    "chart-only colors share a chroma band",
    (palette) =>
      CHART_ONLY.flatMap((name) => {
        const { chroma } = oklch(pick(palette, name));
        const [low, high] = LIMITS.chartChroma;
        return chroma < low || chroma > high
          ? [`${name} has C ${chroma.toFixed(3)}.`]
          : [];
      }),
  ],
  [
    "gray-0 labels are readable on every chart series",
    (palette) =>
      CHART_SERIES.flatMap((name) => {
        const ratio = contrast(pick(palette, "gray-0"), pick(palette, name));
        return ratio < LIMITS.textContrast
          ? [`gray-0 on ${name} is ${ratio.toFixed(2)}:1.`]
          : [];
      }),
  ],
  [
    "every pair of chart series is distinguishable",
    (palette) =>
      pairs(CHART_SERIES).flatMap(([first, second]) => {
        const gap = distance(pick(palette, first), pick(palette, second));
        return gap < LIMITS.seriesDistance
          ? [`${first} and ${second} are ${gap.toFixed(3)} apart.`]
          : [];
      }),
  ],
  [
    "no chart series looks like orange",
    (palette) =>
      CHART_SERIES.flatMap((name) => {
        const gap = distance(pick(palette, name), pick(palette, "orange"));
        return gap < LIMITS.brandDistance
          ? [`${name} is ${gap.toFixed(3)} from orange.`]
          : [];
      }),
  ],
];

const palette: Palette = Object.fromEntries(opaqueColors);

describe("palette color math", () => {
  it.each([
    ["#ff8a3d", 0.75, 0.167],
    ["#ffffff", 1, 0],
    ["#000000", 0, 0],
  ])("converts %s to OKLCH", (hex, lightness, chroma) => {
    expect(oklch(hex).lightness).toBeCloseTo(lightness, 3);
    expect(oklch(hex).chroma).toBeCloseTo(chroma, 3);
  });

  it("measures black against white", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 6);
    expect(contrast("#ffffff", "#000000")).toBeCloseTo(21, 6);
    expect(distance("#000000", "#ffffff")).toBeCloseTo(1, 4);
  });

  it("composites a translucent color in sRGB", () => {
    expect(over("#ffffff", 0.5, "#000000")).toBe("#808080");
  });

  it.each(
    CHART_SERIES.map((name, index): [number, string] => [index + 1, name])
  )("chart series %i is %s", (series, name) => {
    expect(resolve(`var(--chart-${series})`).toLowerCase()).toBe(
      pick(palette, name).toLowerCase()
    );
  });

  it.each(PALETTE_RULES)("%s", (_, rule) => {
    expect(rule(palette)).toStrictEqual([]);
  });

  // The palette before the Color Math Rule: lighter warn and unevenly saturated charts.
  it("rejects a palette that drifts out of the bands", () => {
    const drifted: Palette = {
      ...palette,
      warn: "#f2c94c",
      "chart-orchid": "#ef8fd0",
      "chart-pale-gold": "#f2d38a",
      "chart-ice": "#9fe3f0",
      "chart-sand": "#c9b38f",
    };
    const violations = Object.fromEntries(
      PALETTE_RULES.map(([name, rule]) => [name, rule(drifted)])
    );
    expect(violations).toMatchObject({
      "status colors share a lightness and chroma band": [
        "warn has L 0.850 and C 0.146.",
      ],
      "chart-only colors share a chroma band": [
        "chart-orchid has C 0.140.",
        "chart-ice has C 0.070.",
        "chart-sand has C 0.055.",
      ],
      "every pair of chart series is distinguishable": [
        "gray-10 and chart-ice are 0.116 apart.",
        "chart-pale-gold and chart-sand are 0.110 apart.",
      ],
    });
  });
});
