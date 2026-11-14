import { readFile } from "node:fs/promises";
import { z } from "zod";

const manifest = z.object({
  components: z.record(z.string(), z.object({
    error: z.object({ message: z.string() }).optional(),
    reactDocgenTypescript: z.object({ props: z.record(z.string(), z.unknown()) }).optional(),
    stories: z.array(z.object({
      id: z.string(), snippet: z.string().trim().min(1),
      warning: z.string().optional(), error: z.unknown().optional(),
    })),
  })),
}).parse(JSON.parse(await readFile(process.argv[2] ?? new URL("../storybook-static/manifests/components.json", import.meta.url), "utf8")));

const components = Object.entries(manifest.components);
if (components.length === 0) throw new Error("Storybook published no component documentation");
for (const [id, component] of components) {
  if (component.error) throw new Error(`${id}: ${component.error.message}`);
  if (!component.reactDocgenTypescript || Object.keys(component.reactDocgenTypescript.props).length === 0) {
    throw new Error(`${id}: component props are missing from the agent manifest`);
  }
  for (const story of component.stories) {
    if (story.error !== undefined) throw new Error(`${story.id}: story extraction failed`);
    if (story.warning) throw new Error(`${story.id}: ${story.warning}`);
  }
}
console.log(`Verified ${components.length} component APIs and ${components.reduce((count, [, component]) => count + component.stories.length, 0)} story snippets.`);
