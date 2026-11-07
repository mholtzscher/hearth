import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AutomationCard } from "./AutomationCard";
import { readAutomationDocument } from "./document";

const child = "aut_01920000-0000-7000-8000-00000000000b";
const destination = "ndst_01920000-0000-7000-8000-00000000000a";
const fingerprint = `sha256-v1:${"a".repeat(64)}`;
const definition = `{"name":"Parent","enabled":true,"triggers":[{"id":"motion","kind":"observation","entity_id":"ent_a","dispositions":["applied"],"comparisons":[{"value_pointer":"/level","operator":"lt","operand":9007199254740993}],"previous_comparisons":[{"value_pointer":"/level","operator":"gt","operand":1e-999}]}],"conditions":{"id":"state","kind":"entity_state","entity_id":"ent_a","value_pointer":"/level","operator":"eq","operand":123.456789012345678901},"variables":{"large":9007199254740993},"variables_expr":{"next":"trigger.kind"},"steps":[{"id":"if","kind":"if","conditions":{"id":"check","kind":"expression","expr":"true"},"then":[{"id":"call","kind":"sub_automation","automation_id":"${child}"},{"id":"command","kind":"command","entity_id":"ent_a","operation":"set","parameters":{"level":9007199254740993}}],"else":[{"id":"notification","kind":"notification","destination_id":"${destination}","title":"Notice","message_expr":"vars.next"}]},{"id":"wait","kind":"delay","duration_ms":120000},{"id":"fn","kind":"function","adapter_id":"adapter","function_key":"inspect","contract_fingerprint":"${fingerprint}","arguments":{"n":1e-999}},{"id":"read","kind":"read_state","entity_id":"ent_a","value_pointer":"/level","max_age_seconds":20,"on_unusable":"null"},{"id":"set","kind":"set_variables","assignments":{"n":9007199254740993},"assignments_expr":{"x":"vars.next"}}]}`;
const childDefinition = '{"name":"Entry lights","enabled":false,"triggers":[],"steps":[{"id":"wait","kind":"delay","duration_ms":1000}]}';
const response = (raw: string, status = 200) => new Response(raw, { status, headers: { "content-type": "application/json" } });
function setup(childResponse: () => Promise<Response> = async () => response(`{"id":"${child}","revision":1,"definition":${childDefinition}}`)) {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes(`/automations/${child}`)) return childResponse();
    if (url.startsWith("/v1/entities")) return response('{"items":[{"id":"ent_a","name":"Hall sensor","type":"temperature","support":{}}]}');
    if (url.startsWith("/v1/notification-destinations")) return response(`{"items":[{"id":"${destination}","name":"My phone","available":true}]}`);
    return response('{"items":[]}');
  }));
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
describe("Automation read views", () => {
  it("does not present a structurally invalid document as supported", () => {
    setup();
    const invalid = readAutomationDocument(definition.replace('"id":"wait"', '"id":"if"'));
    render(<AutomationCard document={invalid}/>);
    expect(screen.getByRole("alert").textContent).toContain("Step IDs must be unique");
    expect(screen.queryByLabelText("Sentence sequence")).toBeNull();
  });
  it("shows complete exact values in distinct prose and numbered nested stack views", async () => {
    setup(); render(<AutomationCard document={readAutomationDocument(definition)}/>);
    expect(await screen.findByRole("button", { name: `Run Entry lights (${child})` })).toBeTruthy();
    const sentence = screen.getAllByLabelText("Sentence sequence")[0];
    expect(sentence.tagName).toBe("DIV");
    const article = screen.getByRole("article");
    for (const text of ["Hall sensor (ent_a)", "9007199254740993", "1e-999", "123.456789012345678901", "My phone", "Notice", "vars.next", "Wait 2 minutes", "adapter / inspect", "on unusable: null", "Set variables:"]) expect(article.textContent).toContain(text);
    fireEvent.click(screen.getByRole("button", { name: "Steps" }));
    const stack = screen.getAllByLabelText("Step stack sequence")[0];
    expect(stack.tagName).toBe("OL");
    expect(within(stack).getByText("Then · true", { exact: false })).toBeTruthy();
    expect(stack.textContent).toContain('"level":9007199254740993');
    fireEvent.click(screen.getByRole("button", { name: "Sentence" }));
    expect(screen.getAllByLabelText("Sentence sequence")[0].textContent).toContain('"level":9007199254740993');
  });
  it("shows loading and the current read-only child with navigation", async () => {
    let resolvePreview!: (value: Response) => void; let calls = 0;
    setup(() => ++calls === 1 ? Promise.resolve(response(`{"id":"${child}","revision":1,"definition":${childDefinition}}`)) : new Promise(resolve => { resolvePreview = resolve; }));
    render(<AutomationCard document={readAutomationDocument(definition)}/>);
    fireEvent.click(await screen.findByRole("button", { name: `Run Entry lights (${child})` }));
    expect(screen.getByRole("status").textContent).toContain("Loading");
    resolvePreview(response(`{"id":"${child}","revision":2,"definition":${childDefinition.replace("Entry lights", "Current child")}}`));
    expect(await screen.findByRole("heading", { name: "Current child" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open Automation and history" }).getAttribute("href")).toBe(`/automations/${child}`);
  });
  it("distinguishes a failed child request and ignores a late response after close", async () => {
    let resolvePreview!: (value: Response) => void; let calls = 0;
    setup(() => ++calls === 1 ? Promise.resolve(response(`{"id":"${child}","revision":1,"definition":${childDefinition}}`)) : calls === 2 ? Promise.resolve(response('{"title":"Unavailable"}', 503)) : new Promise(resolve => { resolvePreview = resolve; }));
    render(<AutomationCard document={readAutomationDocument(definition)}/>);
    const button = await screen.findByRole("button", { name: `Run Entry lights (${child})` });
    fireEvent.click(button);
    expect((await screen.findByRole("alert")).textContent).toContain("Could not load Automation");
    fireEvent.click(screen.getByRole("button", { name: "Close preview" }));
    fireEvent.click(button);
    expect(screen.getByRole("status")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Close preview" }));
    resolvePreview(response(`{"id":"${child}","revision":1,"definition":${childDefinition}}`));
    await waitFor(() => expect(screen.queryByLabelText("Referenced Automation")).toBeNull());
  });
});
