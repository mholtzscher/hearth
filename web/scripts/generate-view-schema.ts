import { writeFileSync } from "node:fs";
import { z } from "zod";
import { definitionSchema } from "../src/features/views/catalog/index.ts";

// Named catalog schemas become shared $defs; keep small unnamed shapes inline
// so consumers need not follow a reference for every literal or scalar.
const schema = {
  // Local references stay relative to this resource when HTTP or MCP embeds it.
  $id: "https://hearth.local/views/v1",
  ...z.toJSONSchema(definitionSchema, { target: "draft-2020-12", io: "input" }),
};
writeFileSync(new URL("../../contracts/views/v1/view.schema.json", import.meta.url), `${JSON.stringify(schema, null, 2)}\n`);
