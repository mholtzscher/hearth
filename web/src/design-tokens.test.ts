// @vitest-environment node
import { readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

/**
 * DESIGN.md's front matter and index.css must name the same token values.
 * Change both together; this test says which side drifted.
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

/** DESIGN.md color names whose custom property is not `--<name>`. */
const COLOR_VARIABLES: Record<string, string> = {
  "chart-orchid": "--chart-3",
  "chart-pale-gold": "--chart-5",
  "chart-ice": "--chart-7",
  "chart-sand": "--chart-8",
  primary: "--primary",
};

/**
 * index.css radius tokens that DESIGN.md omits because no application screen
 * uses them. `xs` is unused; `4xl` appears only in the shadcn Badge, which no
 * screen renders. Remove a name here once it is used and documented.
 */
const UNDOCUMENTED_RADII = new Set(["xs", "4xl"]);

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
    "type size %s",
    (name, style) => {
      const declared = theme.get(`--text-${name}`);
      expect(declared, `index.css @theme has no --text-${name}.`).toBeDefined();
      const documented = (style as Tree).fontSize as string;
      expect(
        `${pixels(declared as string)}px`,
        `DESIGN.md ${name} fontSize is ${documented}; index.css --text-${name} is ${declared}.`
      ).toBe(documented);
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
      .map((name) => name.slice("--text-".length));
    expect(
      declared.filter((name) => !(name in section(design, "typography"))),
      "Add these index.css text sizes to DESIGN.md typography."
    ).toStrictEqual([]);
  });

  it("documents every used radius in index.css", () => {
    const declared = [...theme.keys()]
      .filter((name) => name.startsWith("--radius-"))
      .map((name) => name.slice("--radius-".length));
    expect(
      [...UNDOCUMENTED_RADII].filter((name) => !declared.includes(name)),
      "Remove radii that index.css no longer declares from UNDOCUMENTED_RADII."
    ).toStrictEqual([]);
    expect(
      declared.filter(
        (name) =>
          !UNDOCUMENTED_RADII.has(name) && !(name in section(design, "rounded"))
      ),
      "Add these index.css radii to DESIGN.md rounded."
    ).toStrictEqual([]);
  });
});
