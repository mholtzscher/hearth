import { readFile } from "node:fs/promises";

import { z } from "zod";

const manifest = z
  .object({
    components: z.record(
      z.string(),
      z.object({
        error: z
          .object({ name: z.string().optional(), message: z.string() })
          .optional(),
        reactDocgenTypescript: z
          .object({ props: z.record(z.string(), z.unknown()) })
          .optional(),
        stories: z.array(
          z.object({
            id: z.string(),
            snippet: z.string().trim().min(1),
            warning: z.string().optional(),
            error: z.unknown().optional(),
          })
        ),
      })
    ),
  })
  .parse(
    JSON.parse(
      await readFile(
        process.argv[2] ??
          new URL(
            "../storybook-static/manifests/components.json",
            import.meta.url
          ),
        "utf-8"
      )
    )
  );

const components = Object.entries(manifest.components);
// These routed pages take no props, so docgen emits no prop metadata. Keep the
// exception explicit: every reusable component must still publish its API.
const parameterlessPages = new Set([
  "pages-commandspage",
  "pages-dashboardlibrary",
  "pages-entitiespage",
  "pages-devicespage",
  "pages-entitydetailpage",
]);
if (components.length === 0) {
  throw new Error("Storybook published no component documentation");
}
for (const [id, component] of components) {
  const noPropsPage = parameterlessPages.has(id);
  if (
    component.error &&
    !(
      noPropsPage &&
      component.error.name === "react-docgen-typescript found no component docs"
    )
  ) {
    throw new Error(`${id}: ${component.error.message}`);
  }
  if (
    !noPropsPage &&
    (!component.reactDocgenTypescript ||
      Object.keys(component.reactDocgenTypescript.props).length === 0)
  ) {
    throw new Error(
      `${id}: component props are missing from the agent manifest`
    );
  }
  for (const story of component.stories) {
    if (story.error !== undefined) {
      throw new Error(`${story.id}: story extraction failed`);
    }
    if (story.warning) {
      throw new Error(`${story.id}: ${story.warning}`);
    }
  }
}
const pageCount = components.filter(([id]) =>
  parameterlessPages.has(id)
).length;
console.log(
  `Verified ${components.length - pageCount} component APIs, ${pageCount} parameterless pages, and ${components.reduce((count, [, component]) => count + component.stories.length, 0)} story snippets.`
);
