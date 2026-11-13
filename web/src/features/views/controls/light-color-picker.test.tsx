import { StrictMode } from "react";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { FieldEntity } from "../bindings/entity";
import { BoundDevice } from "../cards/device/Device";
import { ViewDataProvider } from "../data/provider";
import { KEYBOARD_SETTLE_MS } from "./BrightnessSlider";

const at = "2026-11-13T10:00:00Z";
const settable = { state: {}, operations: { set: {} } };
const entity = (type: string, value: unknown): FieldEntity => ({
  id: `ent_${type}`, type: `hearth.${type}/v1`, name: type, enabled: true, availability: { status: "available" }, support: settable,
  quantities: type === "brightness" ? { "": { unit: "%", label: "%", fraction_digits: 0 } }
    : type === "colortemp" ? { "/value": { unit: "mired", label: "mired", fraction_digits: 0 } }
    : type === "colorhs" ? { "/hue": { unit: "deg", label: "°", fraction_digits: 0 }, "/saturation": { unit: "%", label: "%", fraction_digits: 0 } }
    : type === "colorrgb" ? Object.fromEntries(["red", "green", "blue"].map(channel => [`/${channel}`, { unit: "1", label: "", fraction_digits: 0 }])) : {},
  state: value === undefined ? null : { value, observed_at: at, observation_id: `obs_${type}` },
});
const temperature = () => ({ ...entity("colortemp", { active: true, value: 370 }), support: { state: { minimum: 153, maximum: 499 }, operations: { set: { step: 5 } } } });
type Post = { entityId: string; body: { operation: string; parameters: Record<string, unknown> }; respond: (response: Response) => void };

/** Real view parsing/cache/queue, with HTTP held at the documented Core boundary. */
async function mount(colors: FieldEntity[], cards = 1) {
  const posts: Post[] = [];
  const entities = [entity("power", true), entity("brightness", 64), ...colors];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    if (init?.method === "POST") return new Promise<Response>(respond => posts.push({
      entityId: String(input).split("/")[3], body: JSON.parse(String(init.body)), respond,
    }));
    return new Response(JSON.stringify({ id: "dev_lamp", name: "Lamp", entities, next_entity_cursor: null }));
  });
  render(<StrictMode><ViewDataProvider>{Array.from({ length: cards }, (_, i) => <BoundDevice key={i} device={{ kind: "device", device_id: "dev_lamp" }} />)}</ViewDataProvider></StrictMode>, { wrapper: MemoryRouter });
  await act(() => vi.advanceTimersByTimeAsync(0));
  const settle = async (index = 0, status = 200) => {
    const post = posts[index];
    if (status === 200) {
      for (const source of colors) {
        if (source.id === post.entityId) source.state = { value: { active: true, ...post.body.parameters }, observed_at: "2026-11-13T10:00:01Z", observation_id: `obs_${index}` };
        else if (source.type !== "hearth.colormode/v1" && source.state && typeof source.state.value === "object") source.state.value = { ...source.state.value, active: false };
      }
      post.respond(new Response(JSON.stringify({ command_id: `cmd_${index}`, status: "satisfied", observation_id: `obs_${index}`, value: post.body.parameters })));
    } else post.respond(new Response(JSON.stringify({ title: "Problem", status }), { status }));
    await act(() => vi.advanceTimersByTimeAsync(0));
    await act(() => vi.advanceTimersByTimeAsync(0));
  };
  return { posts, entities, settle };
}
const open = () => { fireEvent.click(screen.getByRole("button", { name: /Lamp: Adjust/ })); return screen.getByRole("dialog", { name: "Light color" }); };
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-13T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });

it("only offers supported controls, and sends nothing on render, refresh, opening, or mode selection", async () => {
  const core = await mount([temperature(), entity("colorhs", { active: false, hue: 205, saturation: 80 })]);
  const picker = open();
  fireEvent.click(within(picker).getByRole("button", { name: "Color" }));
  await act(() => vi.advanceTimersByTimeAsync(20_000));
  expect(core.posts).toHaveLength(0);
  expect(within(picker).getByRole("slider", { name: "Color wheel" })).toBeTruthy();
  fireEvent.click(within(picker).getByRole("button", { name: "Close color picker" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("sends one temperature Command on release with absolute-step bounds, preserving the observed reading", async () => {
  const core = await mount([temperature()]);
  const picker = open(), slider = within(picker).getByRole("slider", { name: "Color temperature" });
  expect(slider.getAttribute("min")).toBe("-495");
  expect(slider.getAttribute("max")).toBe("-155");
  expect(slider.getAttribute("step")).toBe("5");
  fireEvent.pointerDown(slider);
  fireEvent.change(slider, { target: { value: "-300" } });
  fireEvent.change(slider, { target: { value: "-250" } });
  expect(core.posts).toHaveLength(0);
  fireEvent.pointerUp(slider);
  await act(() => vi.advanceTimersByTimeAsync(0));
  expect(core.posts.map(post => [post.entityId, post.body])).toEqual([["ent_colortemp", { operation: "set", parameters: { value: 250 } }]]);
  expect(within(screen.getByRole("article")).getByText("Color temperature 370 mired")).toBeTruthy();
  expect(within(picker).getByText("Setting white to 250 mired…")).toBeTruthy();
  await core.settle();
  expect(within(screen.getByRole("article")).getByText("Color temperature 250 mired")).toBeTruthy();
});

it("keeps native RGB values exact and shares pending/failure state after the picker closes", async () => {
  const core = await mount([entity("colorrgb", { active: true, red: 255, green: 0, blue: 0 })]);
  const picker = open();
  for (const [label, value] of [["R", "128"], ["G", "32"], ["B", "16"]]) fireEvent.change(within(picker).getByRole("spinbutton", { name: label }), { target: { value } });
  fireEvent.submit(within(picker).getByRole("button", { name: "Set RGB" }).closest("form")!);
  await act(() => vi.advanceTimersByTimeAsync(0));
  expect(core.posts[0].body.parameters).toEqual({ red: 128, green: 32, blue: 16 });
  fireEvent.click(within(picker).getByRole("button", { name: "Close color picker" }));
  expect(screen.getByText("Setting color to #802010…")).toBeTruthy();
  await core.settle(0, 502);
  expect(screen.getByRole("alert").textContent).toContain("The light rejected the change.");
  const reopened = open();
  expect(within(reopened).getByRole("spinbutton", { name: "R" }).getAttribute("value")).toBe("255");
  fireEvent.change(within(reopened).getByRole("textbox", { name: "Hex" }), { target: { value: "#000000" } });
  fireEvent.submit(within(reopened).getByRole("button", { name: "Set hex" }).closest("form")!);
  await act(() => vi.advanceTimersByTimeAsync(0));
  expect(core.posts[1].body.parameters).toEqual({ red: 0, green: 0, blue: 0 });
  await core.settle(1);
  expect(within(reopened).queryByRole("alert")).toBeNull();
  expect(within(screen.getByRole("article")).getByText("RGB 0, 0, 0")).toBeTruthy();
});

it("sends settled HS parameters to an inactive color Entity, never a separate mode Command", async () => {
  const core = await mount([temperature(), entity("colorhs", { active: false, hue: 359, saturation: 100 })]);
  const picker = open();
  fireEvent.click(within(picker).getByRole("button", { name: "Color" }));
  const hue = within(picker).getByRole("slider", { name: "Hue" });
  for (const value of ["0", "1", "2"]) fireEvent.change(hue, { target: { value } });
  await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS));
  expect(core.posts.map(post => [post.entityId, post.body.parameters])).toEqual([["ent_colorhs", { hue: 2, saturation: 100 }]]);
  await core.settle();
  expect(within(screen.getByRole("article")).queryByText("Color temperature 370 mired")).toBeNull();
});

it.each([
  [{ red: 255, green: 255, blue: 255 }, { red: 0, green: 255, blue: 0 }],
  [{ red: 1, green: 1, blue: 1 }, { red: 0, green: 1, blue: 0 }],
])("preserves selected hue when RGB cannot yet encode it: %j", async (initial, expected) => {
  const core = await mount([entity("colorrgb", { active: true, ...initial })]);
  const picker = open(), hue = within(picker).getByRole("slider", { name: "Hue" });
  fireEvent.change(hue, { target: { value: "120" } });
  expect(hue.getAttribute("aria-valuetext")).toBe("120°");
  fireEvent.change(within(picker).getByRole("slider", { name: "Saturation" }), { target: { value: "100" } });
  await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS));
  expect(core.posts[0].body.parameters).toEqual(expected);
});

it("sends an explicit observed white value even while the other mode is pending", async () => {
  const core = await mount([temperature(), entity("colorhs", { active: false, hue: 200, saturation: 80 })]);
  const picker = open();
  fireEvent.click(within(picker).getByRole("button", { name: "Color" }));
  fireEvent.change(within(picker).getByRole("slider", { name: "Hue" }), { target: { value: "210" } });
  await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS));
  fireEvent.click(within(picker).getByRole("button", { name: "White" }));
  fireEvent.submit(within(picker).getByRole("button", { name: "Set white" }).closest("form")!);
  await act(() => vi.advanceTimersByTimeAsync(0));
  expect(core.posts.map(post => [post.entityId, post.body.parameters])).toEqual([
    ["ent_colorhs", { hue: 210, saturation: 80 }], ["ent_colortemp", { value: 370 }],
  ]);
});

it("has one accessible announcement owner while open and after closing", async () => {
  const core = await mount([entity("colorrgb", { active: true, red: 255, green: 0, blue: 0 })]);
  const picker = open();
  fireEvent.change(within(picker).getByRole("textbox", { name: "Hex" }), { target: { value: "#802010" } });
  fireEvent.submit(within(picker).getByRole("button", { name: "Set hex" }).closest("form")!);
  await act(() => vi.advanceTimersByTimeAsync(0));
  const pending = () => screen.getAllByRole("status").filter(status => status.textContent === "Setting color to #802010…");
  expect(pending()).toHaveLength(1);
  fireEvent.click(within(picker).getByRole("button", { name: "Close color picker" }));
  expect(pending()).toHaveLength(1);
  await core.settle(0, 502);
  expect(screen.getAllByRole("alert")).toHaveLength(1);
  open();
  expect(screen.getAllByRole("alert")).toHaveLength(1);
});

it("supports wheel keyboard wrap and rejects invalid exact entries without sending", async () => {
  const core = await mount([entity("colorhs", { active: true, hue: 359, saturation: 80 })]);
  const picker = open(), wheel = within(picker).getByRole("slider", { name: "Color wheel" });
  fireEvent.keyDown(wheel, { key: "ArrowRight" });
  await act(() => vi.advanceTimersByTimeAsync(KEYBOARD_SETTLE_MS));
  expect(core.posts[0].body.parameters).toEqual({ hue: 0, saturation: 80 });
  await core.settle();
  fireEvent.change(within(picker).getByRole("textbox", { name: "Hex" }), { target: { value: "bad" } });
  fireEvent.submit(within(picker).getByRole("button", { name: "Set hex" }).closest("form")!);
  fireEvent.change(within(picker).getByRole("spinbutton", { name: "R" }), { target: { value: "256" } });
  fireEvent.submit(within(picker).getByRole("button", { name: "Set RGB" }).closest("form")!);
  expect(core.posts).toHaveLength(1);
});

it("leaves unavailable Entities controllable but disables disabled Entities", async () => {
  const color = entity("colorrgb", { active: true, red: 255, green: 0, blue: 0 });
  color.availability.status = "unavailable";
  await mount([color]);
  expect((within(open()).getByRole("slider", { name: "Hue" }) as HTMLInputElement).disabled).toBe(false);
  cleanup();
  color.enabled = false;
  const core = await mount([color]);
  const picker = open();
  expect((within(picker).getByRole("slider", { name: "Hue" }) as HTMLInputElement).disabled).toBe(true);
  expect(within(picker).getByText("This Entity is disabled.")).toBeTruthy();
  expect(core.posts).toHaveLength(0);
});

it("uses the established modal phone sheet and omits the trigger without color support", async () => {
  await mount([entity("colorxy", undefined)]);
  expect(screen.queryByRole("button", { name: /Adjust/ })).toBeNull();
  cleanup();
  vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener() {}, removeEventListener() {} }));
  await mount([temperature()]);
  const picker = open();
  expect(picker.getAttribute("aria-modal")).toBe("true");
  expect(picker.classList.contains("view-source-sheet")).toBe(true);
  expect(within(picker).queryByRole("button", { name: "Color" })).toBeNull();
});
