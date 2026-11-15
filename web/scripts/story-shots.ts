import { spawnSync } from "node:child_process";
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { loadCsf } from "storybook/internal/csf-tools";
import { combineTags } from "storybook/internal/csf";
import { userOrAutoTitleFromSpecifier } from "storybook/preview-api";

/**
 * Temporary tooling for the design-system migration. It proves that a change
 * kept the pixels of every story, and reports which stylesheet classes no story
 * renders. Delete it with the last handwritten stylesheet.
 *
 *   story-shots capture <dir>                   screenshot every story into <dir>
 *   story-shots compare <base-dir> <branch-dir> exit 1 if any story's image differs
 *   story-shots coverage <dir>                  exit 1 if a stylesheet class was never rendered
 */

const web = path.resolve(import.meta.dirname, "..");

/** Stylesheet classes that no story needs to render, each with its reason. */
const UNRENDERED_ALLOWED: Record<string, string> = {};

/** pnpm runs this script from web/, so resolve a relative directory from where the caller started. */
const resolve = (directory: string) => path.resolve(process.env.INIT_CWD ?? process.cwd(), directory);

const images = (directory: string) => readdirSync(directory).filter(file => file.endsWith(".png")).sort();

function capture(target: string) {
  const directory = resolve(target);
  mkdirSync(directory, { recursive: true });
  if (readdirSync(directory).length > 0) throw new Error(`Capture needs an empty directory: ${directory}`);
  const run = spawnSync("pnpm", ["run", "test-storybook"], {
    cwd: web,
    stdio: "inherit",
    env: { ...process.env, VITE_HEARTH_STORY_SHOTS: directory },
  });
  const imageCount = images(directory).length;
  console.log(`\n${imageCount} screenshots in ${directory}`);
  const skipped = skippedStories();
  if (skipped.length > 0) console.log(`Stories tagged needs-work (not captured):\n${skipped.map(id => `  ${id}`).join("\n")}`);
  if (run.status === 0 && imageCount > 0) writeFileSync(path.join(directory, "complete.json"), JSON.stringify({ imageCount }));
  if (imageCount === 0) throw new Error("No story screenshots were captured");
  process.exit(run.status ?? 1);
}

/** Interrupted or failed runs leave images behind but cannot serve as a baseline. */
function completedImages(directory: string): string[] {
  const captured = images(directory);
  const { imageCount } = JSON.parse(readFileSync(path.join(directory, "complete.json"), "utf8"));
  if (captured.length === 0 || imageCount !== captured.length) throw new Error(`Incomplete capture: ${directory}`);
  return captured;
}

/** Use Storybook's title and tag rules, including story-level removal of a meta tag. */
function skippedStories(): string[] {
  return readdirSync(path.join(web, "src"), { recursive: true, encoding: "utf8" })
    .filter(file => /\.stories\.(js|jsx|mjs|ts|tsx)$/.test(file))
    .flatMap(file => {
      const fileName = `./src/${file.split(path.sep).join("/")}`;
      const csf = loadCsf(readFileSync(path.join(web, fileName), "utf8"), {
        fileName,
        makeTitle: title => userOrAutoTitleFromSpecifier(fileName, {
          directory: "./src", files: "**/*.stories.*", titlePrefix: "", importPathMatcher: /^\.\/src\//,
        }, title)!,
      }).parse();
      return csf.stories.filter(story => combineTags(...csf.meta?.tags ?? [], ...story.tags ?? []).includes("needs-work"))
        .map(story => story.id);
    }).sort();
}

function compare(baseTarget: string, branchTarget: string) {
  const [baseDirectory, branchDirectory] = [resolve(baseTarget), resolve(branchTarget)];
  const base = completedImages(baseDirectory);
  const branch = new Set(completedImages(branchDirectory));
  const shared = base.filter(file => branch.has(file));
  if (shared.length === 0) throw new Error(`No story has an image in both ${baseDirectory} and ${branchDirectory}`);
  const changed = shared.filter(file =>
    !readFileSync(path.join(baseDirectory, file)).equals(readFileSync(path.join(branchDirectory, file))));
  const onlyBase = base.filter(file => !branch.has(file));
  const onlyBranch = [...branch].filter(file => !base.includes(file));

  const figure = (file: string) => `<section><h2>${file}</h2><div>`
    + `<figure><img src="${pathToFileURL(path.join(baseDirectory, file))}"><figcaption>base</figcaption></figure>`
    + `<figure><img src="${pathToFileURL(path.join(branchDirectory, file))}"><figcaption>branch</figcaption></figure>`
    + `</div></section>`;
  writeFileSync(path.join(branchDirectory, "report.html"), `<!doctype html><meta charset="utf-8"><title>Story differences</title>`
    + `<style>body{background:#111;color:#eee;font:14px sans-serif}div{display:flex;gap:16px;align-items:flex-start}`
    + `figure{margin:0;flex:1;min-width:0}img{max-width:100%;border:1px solid #444}</style>`
    + `<h1>${changed.length} of ${shared.length} images differ</h1>${changed.map(figure).join("")}`);

  const list = (title: string, files: string[]) => { if (files.length > 0) console.log(`${title}:\n${files.map(file => `  ${file}`).join("\n")}`); };
  list("Only in the base", onlyBase);
  list("Only in the branch", onlyBranch);
  list("Different", changed);
  console.log(`${changed.length} of ${shared.length} images differ.${changed.length > 0 ? ` See ${path.join(branchDirectory, "report.html")}` : ""}`);
  process.exit(changed.length + onlyBase.length + onlyBranch.length > 0 ? 1 : 0);
}

/** The class names in a stylesheet's selectors. At-rule preludes and declarations hold none. */
function classNames(css: string): string[] {
  const names = new Set<string>();
  let prelude = "";
  for (const char of css.replace(/\/\*[\s\S]*?\*\//g, "")) {
    if (char === "{") {
      const selectors = prelude.trim();
      if (!selectors.startsWith("@")) {
        for (const match of selectors.replace(/\[[^\]]*\]/g, "").matchAll(/\.(-?[A-Za-z_][\w-]*)/g)) names.add(match[1]);
      }
      prelude = "";
    } else if (char === ";" || char === "}") prelude = "";
    else prelude += char;
  }
  return [...names];
}

function coverage(target: string) {
  const directory = resolve(target);
  completedImages(directory);
  const rendered = new Set(Object.values(JSON.parse(readFileSync(path.join(directory, "classes.json"), "utf8")) as Record<string, string[]>).flat());
  const stylesheets = [...readdirSync(path.join(web, "src"), { recursive: true, encoding: "utf8" }).map(file => path.join("src", file)),
    ...readdirSync(path.join(web, ".storybook"), { encoding: "utf8" }).map(file => path.join(".storybook", file))]
    .filter(file => file.endsWith(".css")).sort();
  let defined = 0;
  let missing = 0;
  for (const stylesheet of stylesheets) {
    const names = classNames(readFileSync(path.join(web, stylesheet), "utf8"));
    const unrendered = names.filter(name => !rendered.has(name) && !(name in UNRENDERED_ALLOWED)).sort();
    defined += names.length;
    missing += unrendered.length;
    if (unrendered.length > 0) console.log(`${stylesheet}: ${unrendered.length} of ${names.length} classes not rendered\n${unrendered.map(name => `  ${name}`).join("\n")}`);
  }
  console.log(`${missing} of ${defined} stylesheet classes were not rendered by any story.`);
  process.exit(missing > 0 ? 1 : 0);
}

const [command, ...targets] = process.argv.slice(2);
if (command === "capture" && targets.length === 1) capture(targets[0]);
else if (command === "compare" && targets.length === 2) compare(targets[0], targets[1]);
else if (command === "coverage" && targets.length === 1) coverage(targets[0]);
else {
  console.error("Usage: story-shots capture <dir> | compare <base-dir> <branch-dir> | coverage <dir>");
  process.exit(2);
}
