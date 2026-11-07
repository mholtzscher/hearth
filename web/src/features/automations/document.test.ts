import { describe, expect, it } from "vitest";
import { automationDocumentIssues, documentValue, editAutomationDocument, newAutomationDraft, readAutomationDocument, sequenceDocument, serializeAutomationDocument } from "./document";
import { editJsonSource } from "../../api/automation-json";

const literal = String.raw`{"large":9007199254740993,"fraction":123.456789012345678901,"exponent":1E-999,"escaped":"\u0031"}`;
const leaf = (id: string) => `{"id":"${id}","kind":"command","entity_id":"ent_example","operation":"set","parameters":${literal}}`;
const condition = `{"id":"state","kind":"entity_state","entity_id":"ent_example","value_pointer":"","operator":"eq","operand":9007199254740993}`;
const source = `{"name":"Exact","enabled":false,"triggers":[],"variables":${literal},"steps":[${leaf("root")},{"id":"if","kind":"if","conditions":${condition},"then":[${leaf("then")}],"else":[${leaf("else")}]},{"id":"choose","kind":"choose","branches":[{"id":"first","conditions":${condition},"steps":[${leaf("branch")}]}],"default":[${leaf("default")}]}]}`;

describe("exact Automation authoring document", () => {
  it("edits names and nested Conditions while retaining sibling tokens across all arms", () => {
    const document = editAutomationDocument(readAutomationDocument(source), ["name"], '"Renamed"');
    expect(document.source.match(/9007199254740993/g)).toHaveLength(8);
    expect(document.source).toContain(literal);
    const next = editAutomationDocument(document, ["steps", 1, "conditions", "operator"], '"ne"');
    expect(serializeAutomationDocument(next, 3)).toBe(`{"expected_revision":3,"definition":${source.replace('"Exact"', '"Renamed"').replace('"operator":"eq"', '"operator":"ne"')}}`);
  });
  it("moves and removes children from only their owning sequence", () => {
    const original = readAutomationDocument(source);
    const moved = sequenceDocument(original, ["steps"], children => [children[2], children[0], children[1]]);
    expect(documentValue(moved, ["steps", 0, "default", 0, "parameters"])).toBe(literal);
    expect(documentValue(moved, ["steps", 2, "then", 0, "parameters"])).toBe(literal);
    const removed = sequenceDocument(moved, ["steps"], children => children.slice(1));
    expect(removed.definition.steps.map(step => step.id)).toEqual(["root", "if"]);
    expect(original.source).toBe(source);
  });
  it("adds and removes optional fields without altering escaped keys or opaque numbers", () => {
    const raw = String.raw`{"en\u0061bled":true,"data":{"x":9007199254740993},"expr":"\u0076ars.x"}`;
    const changed = editJsonSource(raw, ["enabled"], "false");
    expect(changed).toContain('"en\\u0061bled":false');
    expect(editJsonSource(editJsonSource(changed, ["new"], '"ok"'), ["new"], undefined)).toBe(changed);
    expect(editJsonSource('{"x":1,"x":2,"other":1e-999}', ["x"], undefined)).toBe('{"other":1e-999}');
  });
  it("uses the canonical schema for all nine Step kinds and rejects mixed literal/expression bindings", () => {
    const draft = newAutomationDraft();
    expect(draft.document.definition.enabled).toBe(false);
    for (const source of ['{"kind":"delay","id":"d","duration_ms":0}', '{"kind":"command","id":"c","entity_id":"e","operation":"set","parameters":{},"parameters_expr":"vars.x"}', '{"kind":"if","id":"i","conditions":{"id":"x","kind":"expression","expr":"true"},"then":[]}']) {
      expect(() => editAutomationDocument(draft.document, ["steps", 0], source)).toThrow();
    }
    expect(() => readAutomationDocument(draft.document.source.replace('"name"', '"unknown"'))).toThrow();
  });
  it("blocks duplicate IDs and oversized documents before writes", () => {
    const document = readAutomationDocument(source.replace('"id":"then"', '"id":"root"'));
    expect(automationDocumentIssues(document)).toContainEqual({ path: "/steps/1/then/0/id", message: "Step IDs must be unique across all paths." });
    expect(() => serializeAutomationDocument(document)).toThrow("Step IDs");
    expect(() => readAutomationDocument(" ".repeat(128 * 1024 + 1))).toThrow("128 KiB");
    expect(() => serializeAutomationDocument(newAutomationDraft().document, 0)).toThrow("revision");
  });
});
