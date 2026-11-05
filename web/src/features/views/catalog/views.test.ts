import { describe, expect, it } from "vitest";
import examples from "../../../../../contracts/views/v1/examples.json";
import { parseDefinition, readDefinition } from "./compatibility";

describe("view contracts", () => {
  for (const example of examples) {
    it(example.name, () => expect(parseDefinition(example.definition) !== null).toBe(example.valid));
  }
  it("preserves unsupported nodes beside supported siblings", () => {
    const raw = { schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Future", spec: {
      root: "root", elements: {
        root: { type: "Stack", props: { gap: "medium" }, children: ["known", "future"] },
        known: { type: "Note", props: { text: "Still readable" } },
        future: { type: "Future", props: { secret: { untouched: true } } },
      },
    } };
    const parsed = readDefinition(raw);
    expect(parsed.status).toBe("partial");
    expect(parsed.raw).toBe(raw);
    if (parsed.status === "partial") {
      expect(parsed.elements.known?.type).toBe("Note");
      expect(parsed.elements.future).toBeNull();
    }
  });
  it("rejects shared children and cycles", () => {
    for (const children of [["leaf", "leaf"], ["root"]]) {
      expect(parseDefinition({ schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Bad", spec: {
        root: "root", elements: {
          root: { type: "Stack", props: { gap: "small" }, children },
          leaf: { type: "Note", props: { text: "N" } },
        },
      } })).toBeNull();
    }
  });
  it("counts title code points consistently", () => {
    const base = examples[0].definition;
    expect(parseDefinition({ ...base, title: "🌡".repeat(80) })).not.toBeNull();
    expect(parseDefinition({ ...base, title: "🌡".repeat(81) })).toBeNull();
  });
});
