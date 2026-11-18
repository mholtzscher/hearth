// @vitest-environment node
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { cn, RADIUS_TOKENS, TEXT_TOKENS } from "./lib/utils.ts";

/**
 * Keep the dashboard on one design system (CODING_STANDARDS.md, Dashboard design
 * system). Only the specialized BrightnessSlider and LightColorPicker inputs
 * remain outside the low-level field components. Lower a count if an exception
 * disappears; never raise one.
 */

/** Selectors that contain a class, by stylesheet. A stylesheet that is not listed may hold none. */
const CLASS_SELECTORS: Record<string, number> = {};

/** Raw <select>, <textarea>, and <input> elements outside field wrappers, by directory. */
const RAW_CONTROLS: Record<string, number> = {
  "src/features": 5,
};

/**
 * Bordered, card-radius surfaces outside components, by file. The composer, a
 * sent message, and unsupported view content are not cards (DESIGN.md).
 */
const HAND_ROLLED_CARDS: Record<string, number> = {
  "src/features/conversation/messages.tsx": 1,
  "src/features/views/renderer/view-renderer.tsx": 1,
  "src/pages/agent-page.tsx": 1,
};

/** Files outside the low-level wrappers that import radix-ui. */
const RADIX_IMPORTS: string[] = [];

const RAW_CONTROL_WRAPPERS = new Set([
  "src/components/input.tsx",
  "src/components/native-select.tsx",
  "src/components/textarea.tsx",
]);
const RADIX_WRAPPERS = new Set([
  "src/components/accordion.tsx",
  "src/components/badge.tsx",
  "src/components/button.tsx",
  "src/components/card.tsx",
  "src/components/dialog.tsx",
  "src/components/label.tsx",
  "src/components/popover.tsx",
  "src/components/separator.tsx",
  "src/components/switch.tsx",
]);

const web = path.resolve(import.meta.dirname, "..");

/** Paths under `directory`, relative to web/, with forward slashes. */
const files = (directory: string): string[] =>
  readdirSync(path.join(web, directory), {
    recursive: true,
    withFileTypes: true,
  })
    .filter((entry) => entry.isFile())
    .map((entry) =>
      path
        .relative(web, path.join(entry.parentPath, entry.name))
        .split(path.sep)
        .join("/")
    )
    .toSorted();

const read = (file: string) => readFileSync(path.join(web, file), "utf-8");

/** Splits a selector list on the commas that are not inside parentheses. */
const selectors = (list: string): string[] => {
  const parts: string[] = [];
  let depth = 0;
  let current = "";
  for (const char of list) {
    if (char === "(") {
      depth += 1;
    }
    if (char === ")") {
      depth -= 1;
    }
    if (char === "," && depth === 0) {
      parts.push(current);
      current = "";
    } else {
      current += char;
    }
  }
  return [...parts, current];
};

/**
 * Counts the selectors that contain a class. `.a, .b:hover` is two and
 * `:is(.a, .b) p` is one. At-rule preludes such as `@media` are not selectors.
 */
const countClassSelectors = (css: string): number => {
  let count = 0;
  let prelude = "";
  for (const char of css.replaceAll(/\/\*[\s\S]*?\*\//gu, "")) {
    if (char === "{") {
      const list = prelude.trim();
      if (!list.startsWith("@")) {
        // An attribute value such as [href$=".pdf"] is not a class.
        count += selectors(list).filter((selector) =>
          /\.[A-Za-z_-]/u.test(selector.replaceAll(/\[[^\]]*\]/gu, ""))
        ).length;
      }
      prelude = "";
    } else if (char === ";" || char === "}") {
      prelude = "";
    } else {
      prelude += char;
    }
  }
  return count;
};

/** Application source, including shared compositions but excluding tests and stories. */
const applicationSource = (): string[] =>
  files("src").filter(
    (file) =>
      /\.tsx?$/u.test(file) && !/\.(?<kind>test|stories)\.tsx?$/u.test(file)
  );

/**
 * Counts the quoted class lists that draw a card: a card radius together with a
 * plain border. Variant-prefixed classes such as `after:rounded-card` do not count.
 */
const countCardSurfaces = (source: string): number =>
  (source.match(/"[^"\n]*"|`[^`]*`/gu) ?? []).filter((literal) => {
    const classes = literal.slice(1, -1).split(/\s+/u);
    return classes.includes("rounded-card") && classes.includes("border");
  }).length;

const isWrapper = (file: string, rule: "raw-control" | "radix"): boolean =>
  (rule === "raw-control" ? RAW_CONTROL_WRAPPERS : RADIX_WRAPPERS).has(file);

describe("design system wrapper classification", () => {
  it.each([
    ["src/components/input.tsx", true, false],
    ["src/components/native-select.tsx", true, false],
    ["src/components/textarea.tsx", true, false],
    ["src/components/dialog.tsx", false, true],
    ["src/components/badge.tsx", false, true],
    ["src/components/raw-json.tsx", false, false],
    ["src/components/decision-dialog.tsx", false, false],
    ["src/features/views/controls/light-color-picker.tsx", false, false],
    ["src/components/nested/input.tsx", false, false],
  ] as const)(
    "classifies %s by its exact file and rule",
    (file, rawControl, radix) => {
      expect(isWrapper(file, "raw-control")).toBe(rawControl);
      expect(isWrapper(file, "radix")).toBe(radix);
    }
  );
});

describe("countClassSelectors", () => {
  it("counts each selector in a list that contains a class", () => {
    expect(countClassSelectors(".a, .b:hover, p { color: red }")).toBe(2);
  });

  it("counts a selector once however many classes it contains", () => {
    expect(countClassSelectors(":is(.a, .b) .c > p { color: red }")).toBe(1);
  });

  it("counts selectors inside media queries and nested rules", () => {
    expect(
      countClassSelectors(
        "@media (max-width: 720px) { .a { color: red; &:has(.b) { color: blue } } }"
      )
    ).toBe(2);
  });

  it("ignores tokens, element resets, keyframes, comments, and statements", () => {
    expect(
      countClassSelectors(`
      @import "tailwindcss";
      @custom-variant dark (&:is(.dark *));
      /* .commented { color: red } */
      @theme inline { --radius-card: 12px; }
      :root { --gray-0: #000; }
      @layer base { button:disabled { opacity: .5 } a[href$=".pdf"] { color: red } }
      @keyframes fade { 50% { opacity: .4 } to { opacity: 0 } }
    `)
    ).toBe(0);
  });
});

describe("countCardSurfaces", () => {
  it("counts each class list with a card radius and a border", () => {
    expect(
      countCardSurfaces(
        `<div className="rounded-card border bg-card" /><li className={\`rounded-card border \${x}\`} />`
      )
    ).toBe(2);
  });

  it("ignores controls, variant-prefixed radii, and radii without a border", () => {
    expect(
      countCardSurfaces(
        '<input className="rounded-md border" /><span className="after:rounded-card border" /><span className="rounded-card bg-card" />'
      )
    ).toBe(0);
  });
});

describe("design system limits", () => {
  it("holds each stylesheet to its class-selector limit", () => {
    const measured = Object.fromEntries(
      [...files("src"), ...files(".storybook")]
        .filter((file) => file.endsWith(".css"))
        .map((file) => [file, countClassSelectors(read(file))] as const)
        .filter(([, count]) => count > 0)
    );
    expect(
      measured,
      "Style elements with Tailwind utilities, not new class selectors. If a count fell, lower its limit to match."
    ).toStrictEqual(CLASS_SELECTORS);
  });

  it("holds raw form controls outside field wrappers to their limit", () => {
    const measured: Record<string, number> = {};
    for (const file of applicationSource()) {
      if (isWrapper(file, "raw-control")) {
        continue;
      }
      const count =
        read(file).match(/<(?<tag>select|textarea|input)(?=[\s/>])/gu)
          ?.length ?? 0;
      if (count === 0) {
        continue;
      }
      const segments = file.split("/");
      // src/features/views/... counts under src/features; src/app.tsx counts under src.
      const directory =
        segments.length > 2 ? segments.slice(0, 2).join("/") : segments[0];
      measured[directory] = (measured[directory] ?? 0) + count;
    }
    expect(
      measured,
      "Use the field and select components in components. If a count fell, lower its limit to match."
    ).toStrictEqual(RAW_CONTROLS);
  });

  it("holds hand-rolled card surfaces to their limit", () => {
    const measured = Object.fromEntries(
      applicationSource()
        .filter((file) => !file.startsWith("src/components/"))
        .map((file) => [file, countCardSurfaces(read(file))] as const)
        .filter(([, count]) => count > 0)
    );
    expect(
      measured,
      "Draw a card with Card from components/card.tsx. If a count fell, lower its limit to match."
    ).toStrictEqual(HAND_ROLLED_CARDS);
  });

  it("keeps radix-ui imports inside low-level wrappers", () => {
    const measured = applicationSource().filter(
      (file) =>
        !isWrapper(file, "radix") && /from\s+["']radix-ui["']/u.test(read(file))
    );
    expect(
      measured,
      "Import the wrapper from components instead of radix-ui. If a file stopped importing it, remove it from the list."
    ).toStrictEqual(RADIX_IMPORTS);
  });
});

describe("Hearth theme tokens", () => {
  const css = read("src/index.css").replaceAll(/\/\*[\s\S]*?\*\//gu, "");

  it("registers every custom text size with cn", () => {
    const names = [...css.matchAll(/--text-(?<name>[\w-]+)\s*:/gu)]
      .map((match) => match[1])
      .toSorted();
    expect(names).toStrictEqual([...TEXT_TOKENS].toSorted());
  });

  it("uses only the radii index.css declares, plus full and none", () => {
    const declared = new Set([
      ...[...css.matchAll(/--radius-(?<name>[\w-]+)\s*:/gu)].map(
        (match) => match[1]
      ),
      "full",
      "none",
    ]);
    const sides = "(?:[trblxy]|tl|tr|br|bl|[se]|ss|se|es|ee)-";
    const unknown = [...files("src"), ...files(".storybook")]
      .filter(
        (file) => /\.(?:tsx?|mdx)$/u.test(file) && !/\.test\.tsx?$/u.test(file)
      )
      .flatMap((file) =>
        [
          ...read(file).matchAll(
            new RegExp(
              `(?<![\\w-])rounded-(?:${sides})?(?<name>[a-z\\d]+)(?![\\w-])`,
              "gu"
            )
          ),
        ]
          .filter((match) => !declared.has(match.groups?.name ?? ""))
          .map((match) => `${file}: ${match[0]}`)
      );
    expect(
      unknown,
      "index.css resets Tailwind's radius scale, so these classes draw square corners. Use rounded-md, rounded-card, or an arbitrary radius."
    ).toStrictEqual([]);
  });

  it("registers every custom radius with cn, excluding Tailwind's built-in names", () => {
    const defaults = readFileSync(
      new URL(import.meta.resolve("tailwindcss/theme.css")),
      "utf-8"
    );
    const builtIn = new Set(
      [...defaults.matchAll(/--radius-(?<name>[\w-]+)\s*:/gu)].map(
        (match) => match[1]
      )
    );
    const names = [...css.matchAll(/--radius-(?<name>[\w-]+)\s*:/gu)]
      .map((match) => match[1])
      .filter((name) => !builtIn.has(name))
      .toSorted();
    expect(names).toStrictEqual([...RADIUS_TOKENS].toSorted());
  });
});

describe("Hearth class merging", () => {
  it.each(TEXT_TOKENS)(
    "keeps text-%s and a text colour in either order",
    (token) => {
      expect(cn(`text-${token}`, "text-foreground")).toBe(
        `text-${token} text-foreground`
      );
      expect(cn("text-foreground", `text-${token}`)).toBe(
        `text-foreground text-${token}`
      );
    }
  );

  it("keeps only the last text size without removing the colour", () => {
    expect(cn("text-label text-foreground", "text-body")).toBe(
      "text-foreground text-body"
    );
    expect(cn("text-body", "text-sm")).toBe("text-sm");
    expect(cn("text-sm", "text-body")).toBe("text-body");
  });

  it("resolves a custom radius against a built-in radius", () => {
    expect(cn("rounded-card", "rounded-md")).toBe("rounded-md");
    expect(cn("rounded-md", "rounded-card")).toBe("rounded-card");
  });

  it("overrides the card radius in each direction", () => {
    expect(cn("rounded-t-xl", "rounded-t-card")).toBe("rounded-t-card");
    expect(cn("rounded-t-card", "rounded-t-xl")).toBe("rounded-t-xl");
    expect(cn("rounded-b-xl", "rounded-b-card")).toBe("rounded-b-card");
    expect(cn("rounded-b-card", "rounded-b-xl")).toBe("rounded-b-xl");
  });

  it("keeps a directional card radius paired with a full radius", () => {
    expect(cn("rounded-t-card", "rounded-md")).toBe("rounded-md");
    expect(cn("rounded-md", "rounded-t-card")).toBe(
      "rounded-md rounded-t-card"
    );
  });

  it("merges named sizes within the same responsive variant", () => {
    expect(
      cn(
        "text-body tablet:text-label",
        "tablet:text-title tablet:text-foreground"
      )
    ).toBe("text-body tablet:text-title tablet:text-foreground");
  });
});
