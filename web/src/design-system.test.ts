// @vitest-environment node
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { cn, RADIUS_TOKENS, TEXT_TOKENS } from "./lib/utils.ts";

/**
 * The dashboard is moving to one design system (CODING_STANDARDS.md, Dashboard
 * design system). These limits hold what remains of the older ways of writing
 * styles and controls. Lower a number when a change removes what it counts, and
 * delete an entry that reaches zero. Never raise one.
 */

/** Selectors that contain a class, by stylesheet. A stylesheet that is not listed may hold none. */
const CLASS_SELECTORS: Record<string, number> = {
  ".storybook/preview.css": 3,
  "src/features/automations/automations.css": 102,
  "src/legacy.css": 874,
};

/** Raw <select>, <textarea>, and <input> elements outside components/ui, by directory. */
const RAW_CONTROLS: Record<string, number> = {
  "src/features": 48,
  "src/pages": 3,
};

/** Files outside components/ui that import radix-ui. */
const RADIX_IMPORTS: string[] = [
  "src/features/views/cards/CardSource.tsx",
  "src/features/views/controls/LightColorPicker.tsx",
];

const web = path.resolve(import.meta.dirname, "..");

/** Paths under `directory`, relative to web/, with forward slashes. */
function files(directory: string): string[] {
  return readdirSync(path.join(web, directory), { recursive: true, withFileTypes: true })
    .filter(entry => entry.isFile())
    .map(entry => path.relative(web, path.join(entry.parentPath, entry.name)).split(path.sep).join("/"))
    .sort();
}

const read = (file: string) => readFileSync(path.join(web, file), "utf8");

/** Splits a selector list on the commas that are not inside parentheses. */
function selectors(list: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let current = "";
  for (const char of list) {
    if (char === "(") depth++;
    if (char === ")") depth--;
    if (char === "," && depth === 0) {
      parts.push(current);
      current = "";
    } else current += char;
  }
  return [...parts, current];
}

/**
 * Counts the selectors that contain a class. `.a, .b:hover` is two and
 * `:is(.a, .b) p` is one. At-rule preludes such as `@media` are not selectors.
 */
function countClassSelectors(css: string): number {
  let count = 0;
  let prelude = "";
  for (const char of css.replace(/\/\*[\s\S]*?\*\//g, "")) {
    if (char === "{") {
      const list = prelude.trim();
      if (!list.startsWith("@")) {
        // An attribute value such as [href$=".pdf"] is not a class.
        count += selectors(list).filter(selector => /\.[A-Za-z_-]/.test(selector.replace(/\[[^\]]*\]/g, ""))).length;
      }
      prelude = "";
    } else if (char === ";" || char === "}") prelude = "";
    else prelude += char;
  }
  return count;
}

/** Application source: TypeScript outside components/ui that is neither a test nor a story. */
function applicationSource(): string[] {
  return files("src").filter(file =>
    /\.tsx?$/.test(file) && !/\.(test|stories)\.tsx?$/.test(file) && !file.startsWith("src/components/ui/"));
}

describe("countClassSelectors", () => {
  it("counts each selector in a list that contains a class", () => {
    expect(countClassSelectors(".a, .b:hover, p { color: red }")).toBe(2);
  });

  it("counts a selector once however many classes it contains", () => {
    expect(countClassSelectors(":is(.a, .b) .c > p { color: red }")).toBe(1);
  });

  it("counts selectors inside media queries and nested rules", () => {
    expect(countClassSelectors("@media (max-width: 720px) { .a { color: red; &:has(.b) { color: blue } } }")).toBe(2);
  });

  it("ignores tokens, element resets, keyframes, comments, and statements", () => {
    expect(countClassSelectors(`
      @import "tailwindcss";
      @custom-variant dark (&:is(.dark *));
      /* .commented { color: red } */
      @theme inline { --radius-card: 12px; }
      :root { --gray-0: #000; }
      @layer base { button:disabled { opacity: .5 } a[href$=".pdf"] { color: red } }
      @keyframes fade { 50% { opacity: .4 } to { opacity: 0 } }
    `)).toBe(0);
  });
});

describe("design system limits", () => {
  it("holds each stylesheet to its class-selector limit", () => {
    const measured = Object.fromEntries([...files("src"), ...files(".storybook")]
      .filter(file => file.endsWith(".css"))
      .map(file => [file, countClassSelectors(read(file))] as const)
      .filter(([, count]) => count > 0));
    expect(measured, "Style elements with Tailwind utilities, not new class selectors. If a count fell, lower its limit to match.")
      .toEqual(CLASS_SELECTORS);
  });

  it("holds raw form controls outside components/ui to their limit", () => {
    const measured: Record<string, number> = {};
    for (const file of applicationSource()) {
      const count = read(file).match(/<(select|textarea|input)(?=[\s/>])/g)?.length ?? 0;
      if (count === 0) continue;
      const segments = file.split("/");
      // src/features/views/... counts under src/features; src/App.tsx counts under src.
      const directory = segments.length > 2 ? segments.slice(0, 2).join("/") : segments[0];
      measured[directory] = (measured[directory] ?? 0) + count;
    }
    expect(measured, "Use the field and select components in components/ui. If a count fell, lower its limit to match.")
      .toEqual(RAW_CONTROLS);
  });

  it("keeps radix-ui imports inside components/ui", () => {
    const measured = applicationSource().filter(file => /from\s+["']radix-ui["']/.test(read(file)));
    expect(measured, "Import the wrapper from components/ui instead of radix-ui. If a file stopped importing it, remove it from the list.")
      .toEqual(RADIX_IMPORTS);
  });
});

describe("Hearth theme tokens", () => {
  const css = read("src/index.css").replace(/\/\*[\s\S]*?\*\//g, "");

  it("registers every custom text size with cn", () => {
    const names = [...css.matchAll(/--text-([\w-]+)\s*:/g)].map(match => match[1]).sort();
    expect(names).toEqual([...TEXT_TOKENS].sort());
  });

  it("registers every custom radius with cn, excluding Tailwind's built-in names", () => {
    const defaults = readFileSync(new URL(import.meta.resolve("tailwindcss/theme.css")), "utf8");
    const builtIn = new Set([...defaults.matchAll(/--radius-([\w-]+)\s*:/g)].map(match => match[1]));
    const names = [...css.matchAll(/--radius-([\w-]+)\s*:/g)]
      .map(match => match[1]).filter(name => !builtIn.has(name)).sort();
    expect(names).toEqual([...RADIUS_TOKENS].sort());
  });
});

describe("Hearth class merging", () => {
  it.each(TEXT_TOKENS)("keeps text-%s and a text colour in either order", token => {
    expect(cn(`text-${token}`, "text-foreground")).toBe(`text-${token} text-foreground`);
    expect(cn("text-foreground", `text-${token}`)).toBe(`text-foreground text-${token}`);
  });

  it("keeps only the last text size without removing the colour", () => {
    expect(cn("text-label text-foreground", "text-body")).toBe("text-foreground text-body");
    expect(cn("text-body", "text-sm")).toBe("text-sm");
    expect(cn("text-sm", "text-body")).toBe("text-body");
  });

  it("resolves a custom radius against a built-in radius", () => {
    expect(cn("rounded-card", "rounded-md")).toBe("rounded-md");
    expect(cn("rounded-md", "rounded-card")).toBe("rounded-card");
  });

  it("resolves directional radius overrides on Card headers and footers", () => {
    expect(cn("rounded-t-xl", "rounded-t-card")).toBe("rounded-t-card");
    expect(cn("rounded-t-card", "rounded-t-xl")).toBe("rounded-t-xl");
    expect(cn("rounded-b-xl", "rounded-b-card")).toBe("rounded-b-card");
    expect(cn("rounded-b-card", "rounded-b-xl")).toBe("rounded-b-xl");
    expect(cn("rounded-t-card", "rounded-md")).toBe("rounded-md");
    expect(cn("rounded-md", "rounded-t-card")).toBe("rounded-md rounded-t-card");
  });

  it("merges named sizes within the same responsive variant", () => {
    expect(cn("text-body tablet:text-label", "tablet:text-title tablet:text-foreground"))
      .toBe("text-body tablet:text-title tablet:text-foreground");
  });
});
