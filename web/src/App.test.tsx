import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from "./App";

beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });

it("keeps connection setup in Development, not the household workspace", async () => {
  vi.stubGlobal("fetch", vi.fn(async input => {
    const path = String(input);
    const body = path === "/readyz" ? { status: "ready" }
      : path === "/healthz" ? { status: "ok" }
      : path === "/v1/agent/conversations" ? { conversations: [] }
      : path.startsWith("/v1/devices?") ? { items: [] } : null;
    if (!body) throw new Error(`Unexpected request ${path}`);
    return new Response(JSON.stringify(body));
  }));
  render(<App />);
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Connection settings" })).toBeNull();
  expect(await screen.findByText("Ready", { exact: true })).toBeTruthy();
  fireEvent.click(screen.getByRole("link", { name: "Development" }));
  expect(await screen.findByText("No devices.")).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "Connection settings" })).toHaveLength(1);
  fireEvent.click(screen.getByRole("link", { name: "Home" }));
  expect(await screen.findByRole("heading", { name: "Home workspace" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Connection settings" })).toBeNull();
});
