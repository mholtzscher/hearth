import { cleanup, fireEvent, render as baseRender, screen, waitFor, within } from "@testing-library/react";
import { useState, type ReactElement } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { AutomationCard } from "./AutomationCard";
import { AutomationEditor } from "./AutomationEditor";
import { readAutomationDocument, type AutomationDocument } from "./document";

const render = (ui: ReactElement) => baseRender(<MemoryRouter>{ui}</MemoryRouter>);
const fingerprint = `sha256-v1:${"b".repeat(64)}`;
const source = '{"name":"Draft","enabled":false,"triggers":[],"conditions":{"id":"state","kind":"entity_state","entity_id":"ent_light","value_pointer":"","operator":"eq","operand":9007199254740993},"steps":[{"id":"command","kind":"command","entity_id":"ent_light","operation":"set","parameters":{"value":20}},{"id":"wait","kind":"delay","duration_ms":1000}]}';
function Harness({ validity = vi.fn() }: { validity?: (valid: boolean) => void }) {
  const [doc, setDoc] = useState<AutomationDocument>(() => readAutomationDocument(source));
  return <><AutomationCard document={doc}><AutomationEditor document={doc} onChange={setDoc} onValidityChange={validity}/></AutomationCard><output data-testid="source">{doc.source}</output></>;
}
const response = (value: unknown) => new Response(JSON.stringify(value), { headers: { "content-type": "application/json" } });
function setup() {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/v1/entities")) return url.includes("cursor=") ? response({ items: [{ id: "ent_other", name: "Other light", type: "hearth.brightness/v1", support: { operations: { set: {} } } }] }) : response({ items: [{ id: "ent_light", name: "Light", type: "hearth.brightness/v1", support: { operations: { set: {} } } }, { id: "ent_read", name: "Read only", type: "hearth.brightness/v1", support: { operations: {} } }], next_cursor: "next" });
    if (url.startsWith("/v1/adapters/adapter/functions")) return url.includes("cursor=") ? response({ items: [{ adapter_id: "adapter", function_key: "inspect", contract_fingerprint: fingerprint, description: "Inspect", input_schema: { type: "object", required: ["target"] }, callable: false }] }) : response({ items: [], next_cursor: "second" });
    if (url.startsWith("/v1/adapters?")) return response({ items: [{ id: "adapter" }] });
    return response({ items: [] });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
it("gates Commands on supported Operations and shows generated State and parameter schemas", async () => {
  const fetcher = setup(); render(<Harness/>);
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes("/v1/entities?limit=200&cursor=next"))).toBe(true));
  const command = screen.getByText("command · command").closest("details")!;
  const entity = within(command).getByLabelText("Entity") as HTMLSelectElement;
  await waitFor(() => expect(within(entity).getByRole("option", { name: /Other light/ })).toBeTruthy());
  expect(within(entity).queryByRole("option", { name: /Read only/ })).toBeNull();
  expect(within(command).getByText(/Hearth brightness\/v1 set parameters/)).toBeTruthy();
  expect(screen.getByText("State fields · canonical units")).toBeTruthy();
  fireEvent.change(entity, { target: { value: "ent_other" } });
  expect(screen.getByTestId("source").textContent).toContain('"entity_id":"ent_other"');
  expect(screen.getByTestId("source").textContent).toContain('"operand":9007199254740993');
});
it("discovers paginated Function descriptors and inserts their real identity and schema", async () => {
  const fetcher = setup(); render(<Harness/>);
  const root = screen.getByRole("heading", { name: "Steps", level: 4 }).closest("section")!;
  fireEvent.change(within(root).getAllByLabelText("Add Step").slice(-1)[0], { target: { value: "function" } });
  await waitFor(() => expect(fetcher.mock.calls.some(([url]) => String(url).includes("functions?limit=200&cursor=second"))).toBe(true));
  await screen.findByRole("option", { name: /adapter \/ inspect/ });
  fireEvent.click(within(root).getByRole("button", { name: "Add to Steps" }));
  const parsed = JSON.parse(screen.getByTestId("source").textContent!);
  expect(parsed.steps[2]).toMatchObject({ kind: "function", adapter_id: "adapter", function_key: "inspect", contract_fingerprint: fingerprint, arguments: {} });
  expect(screen.getByText(/"target"/)).toBeTruthy();
  expect(screen.getByLabelText("Exact JSON Step inspector")).toBeTruthy();
});
it("keeps invalid field text mounted while read style changes", async () => {
  setup(); const validity = vi.fn(); render(<Harness validity={validity}/>);
  const input = screen.getByLabelText("Wait · milliseconds") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "0" } });
  await waitFor(() => expect(validity).toHaveBeenLastCalledWith(false));
  fireEvent.click(screen.getByRole("button", { name: "Steps" }));
  fireEvent.click(screen.getByRole("button", { name: "Sentence" }));
  expect(input.isConnected).toBe(true);
  expect(input.value).toBe("0");
  expect(screen.getByTestId("source").textContent).toBe(source);
});
it("wraps and changes Condition groups without rounding their existing operands", async () => {
  setup(); render(<Harness/>);
  const admission = screen.getByRole("heading", { name: "Admission Conditions" }).closest("section")!;
  fireEvent.change(within(admission).getByLabelText("Condition type"), { target: { value: "all" } });
  let doc = JSON.parse(screen.getByTestId("source").textContent!);
  expect(doc.conditions.kind).toBe("all");
  expect(doc.conditions.children[0].entity_id).toBe("ent_light");
  fireEvent.change(within(admission).getAllByLabelText("Condition type")[0], { target: { value: "none" } });
  doc = JSON.parse(screen.getByTestId("source").textContent!);
  expect(doc.conditions.kind).toBe("not");
  expect(doc.conditions.child.kind).toBe("any");
  expect(screen.getByTestId("source").textContent).toContain('"operand":9007199254740993');
});
