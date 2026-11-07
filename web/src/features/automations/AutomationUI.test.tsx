import { cleanup, fireEvent, render as baseRender, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactElement } from "react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AutomationCard } from "./AutomationCard";
import { AutomationEditor } from "./AutomationEditor";
import { readAutomationDocument, type AutomationDocument } from "./document";

const render = (ui: ReactElement) => baseRender(<MemoryRouter>{ui}</MemoryRouter>);

const childID = "aut_01920000-0000-7000-8000-00000000000b";
const source = `{"name":"Hall","enabled":false,"triggers":[],"steps":[{"id":"first","kind":"delay","duration_ms":1000},{"id":"if","kind":"if","conditions":{"id":"test","kind":"expression","expr":"true"},"then":[{"id":"child","kind":"sub_automation","automation_id":"${childID}"}],"else":[{"id":"command","kind":"command","entity_id":"ent_a","operation":"set","parameters":{"number":9007199254740993}}]}]}`;
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
function installFetch() { vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
  if (String(input).includes(childID)) return new Response(JSON.stringify({ title: "Missing" }), { status: 404 });
  return new Response(JSON.stringify({ items: [], next_cursor: null }));
})); }
function Harness({ initial = readAutomationDocument(source), validity = vi.fn() }: { initial?: AutomationDocument; validity?: (valid: boolean) => void }) {
  const [doc, setDoc] = useState(initial);
  return <><AutomationEditor document={doc} onChange={setDoc} onValidityChange={validity}/><output data-testid="source">{doc.source}</output></>;
}
describe("Automation cards and editor", () => {
  it("does not strand an invalid Step when the whole-definition inspector replaces its path", () => {
    installFetch(); const validity = vi.fn(); render(<Harness validity={validity}/>);
    fireEvent.change(screen.getByLabelText("Wait · milliseconds"), { target: { value: "0" } });
    const replacement = '{"name":"Replacement","enabled":false,"triggers":[],"steps":[{"id":"other","kind":"delay","duration_ms":1}]}';
    const inspector = screen.getByLabelText("Exact JSON definition");
    fireEvent.change(inspector, { target: { value: replacement } });
    expect(screen.getByLabelText("Wait · milliseconds")).toHaveProperty("value", "0");
    expect(inspector).toHaveProperty("value", replacement);
    fireEvent.change(screen.getByLabelText("Wait · milliseconds"), { target: { value: "1000" } });
    fireEvent.change(inspector, { target: { value: replacement + " " } });
    expect(screen.getByTestId("source").textContent).toContain('"id":"other"');
    expect(validity).toHaveBeenLastCalledWith(true);
  });
  it("edits, bounds and removes an optional State Condition age without rounding operands", () => {
    installFetch();
    const source = '{"name":"Age","enabled":false,"triggers":[],"conditions":{"id":"state","kind":"entity_state","entity_id":"ent_a","value_pointer":"","operator":"eq","operand":9007199254740993},"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}';
    render(<Harness initial={readAutomationDocument(source)}/>);
    fireEvent.click(screen.getByRole("button", { name: "Add maximum State age" }));
    const age = screen.getByLabelText("Maximum State age · seconds");
    fireEvent.change(age, { target: { value: "0" } }); expect(age).toHaveProperty("value", "0");
    fireEvent.change(age, { target: { value: "120" } });
    expect(screen.getByTestId("source").textContent).toContain('"max_age_seconds":120');
    fireEvent.click(screen.getByRole("button", { name: "Remove maximum State age" }));
    expect(screen.getByTestId("source").textContent).not.toContain("max_age_seconds");
    expect(screen.getByTestId("source").textContent).toContain("9007199254740993");
  });
  it("switches styles without changing a definition and restores focus after a missing child", async () => {
    installFetch();
    render(<AutomationCard document={readAutomationDocument(source)}/>);
    fireEvent.click(screen.getByRole("button", { name: "Steps" }));
    expect(screen.getByRole("button", { name: "Steps" }).getAttribute("aria-pressed")).toBe("true");
    const opener = screen.getByRole("button", { name: new RegExp(`Inspect .*${childID}`) });
    fireEvent.click(opener);
    expect((await screen.findByRole("alert")).textContent).toContain("Automation not found");
    const arm = opener.closest("details")!; arm.open = false;
    fireEvent.click(screen.getByRole("button", { name: "Close preview" }));
    await waitFor(() => expect(document.activeElement).toBe(opener));
    expect(arm.open).toBe(true);
    const exactDefinition = document.querySelector(".automation-code-block")?.textContent ?? "";
    expect(exactDefinition).toContain('\n  "steps": [');
    expect(exactDefinition.replace(/\s+/g, "")).toBe(source.replace(/\s+/g, ""));
    expect(exactDefinition).toContain("9007199254740993");
  });
  it("keeps invalid field input locally and preserves exact siblings after a valid edit", async () => {
    installFetch(); const validity = vi.fn(); render(<Harness validity={validity}/>);
    const wait = screen.getByLabelText("Wait · milliseconds");
    fireEvent.change(wait, { target: { value: "0" } });
    expect((wait as HTMLInputElement).value).toBe("0");
    expect(screen.getByTestId("source").textContent).toBe(source);
    await waitFor(() => expect(validity).toHaveBeenLastCalledWith(false));
    fireEvent.change(wait, { target: { value: "2500" } });
    await waitFor(() => expect(validity).toHaveBeenLastCalledWith(true));
    expect(screen.getByTestId("source").textContent).toContain('"number":9007199254740993');
    expect(screen.getByTestId("source").textContent).toContain('"duration_ms":2500');
  });
  it("moves Steps within their own sequence using exact retained values", () => {
    installFetch(); render(<Harness/>);
    const first = screen.getByText("delay · first").closest("details")!;
    expect(within(first).getByRole("button", { name: "Remove Step first from Steps" }).textContent).toBe("Remove Step");
    expect(within(first).getByText("Actions for first · Steps").textContent).toBe("Actions for first · Steps");
    fireEvent.click(within(first).getByRole("button", { name: "Move down Step first in Steps" }));
    const result = screen.getByTestId("source").textContent!;
    expect(result.indexOf('"id":"if"')).toBeLessThan(result.indexOf('"id":"first"'));
    expect(result).toContain('"number":9007199254740993');
  });
  it("adds and reorders nested Choose paths without rewriting existing values", () => {
    installFetch(); render(<Harness/>);
    const root = screen.getByRole("heading", { name: "Steps", level: 4 }).closest("section")!;
    fireEvent.change(within(root).getAllByLabelText("Add Step").slice(-1)[0], { target: { value: "choose" } });
    fireEvent.click(within(root).getByRole("button", { name: "Add to Steps" }));
    fireEvent.click(screen.getByRole("button", { name: "Add path" }));
    const second = screen.getByText(/Path 2 ·/).closest("details")!;
    fireEvent.click(within(second).getByRole("button", { name: "Move path up" }));
    const result = JSON.parse(screen.getByTestId("source").textContent!);
    expect(result.steps[2].branches).toHaveLength(2);
    expect(result.steps[2].branches[0].id).toBe("branch_2");
    expect(screen.getByTestId("source").textContent).toContain('"number":9007199254740993');
  });
});
