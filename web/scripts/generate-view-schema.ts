import { writeFileSync } from "node:fs";
import { z } from "zod";
import { definitionSchema } from "../src/features/views/catalog/index.ts";

const schema = z.toJSONSchema(definitionSchema, { target: "draft-2020-12", io: "input" });
writeFileSync(new URL("../../contracts/views/v1/view.schema.json", import.meta.url), `${JSON.stringify(schema, null, 2)}\n`);
