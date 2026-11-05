import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { setBaseUrl } from "../api/client";
import { ServerStatus } from "./ServerStatus";

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
afterEach(() => { cleanup(); setBaseUrl(""); vi.useRealTimers(); vi.unstubAllGlobals(); });

it("shows a pending check without claiming readiness", async () => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(done => { resolve = done; })));
  render(<ServerStatus />);
  expect(screen.getByRole("status").textContent).toBe("Hearth server Checking…");
  await act(async () => resolve(json({ status: "ready" })));
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
});

it.each([
  [200, "ready", "Ready"],
  [503, "not_ready", "Not ready"],
  [200, "not_ready", "Not ready"],
  [200, "unexpected", "Status unknown"],
])("reports HTTP %i / %s as %s", async (status, value, label) => {
  const fetcher = vi.fn(async (_input: RequestInfo | URL) => json({ status: value }, status));
  vi.stubGlobal("fetch", fetcher);
  render(<ServerStatus />);
  await act(async () => {});
  expect(screen.getByRole("status").textContent).toBe(`Hearth server ${label}`);
  expect(fetcher.mock.calls[0][0]).toBe("/readyz");
});

it("does not keep a stale Ready label after a failed check, and recovers", async () => {
  vi.useFakeTimers();
  const fetcher = vi.fn()
    .mockResolvedValueOnce(json({ status: "ready" }))
    .mockRejectedValueOnce(new TypeError("Failed to fetch"))
    .mockResolvedValueOnce(json({ status: "ready" }));
  vi.stubGlobal("fetch", fetcher);
  render(<ServerStatus />);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Status unavailable");
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
  expect(fetcher).toHaveBeenCalledTimes(3);
});

it("keeps the last completed check visible while the next poll is pending", async () => {
  vi.useFakeTimers();
  let resolveNext!: (response: Response) => void;
  const fetcher = vi.fn().mockResolvedValueOnce(json({ status: "ready" }))
    .mockImplementation(() => new Promise<Response>(resolve => { resolveNext = resolve; }));
  vi.stubGlobal("fetch", fetcher);
  render(<ServerStatus />);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
  await act(async () => resolveNext(json({ status: "not_ready" }, 503)));
  expect(screen.getByRole("status").textContent).toBe("Hearth server Not ready");
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Not ready");
  await act(async () => resolveNext(json({ status: "ready" })));
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
});

it("does not carry a previous server's readiness into a new connection", async () => {
  let resolveNext!: (response: Response) => void;
  const fetcher = vi.fn().mockResolvedValueOnce(json({ status: "ready" }))
    .mockImplementationOnce(() => new Promise<Response>(resolve => { resolveNext = resolve; }));
  vi.stubGlobal("fetch", fetcher);
  render(<ServerStatus />);
  await act(async () => {});
  expect(screen.getByRole("status").textContent).toBe("Hearth server Ready");
  await act(async () => { setBaseUrl("http://new-server"); });
  expect(screen.getByRole("status").textContent).toBe("Hearth server Checking…");
  await act(async () => resolveNext(json({ status: "not_ready" }, 503)));
  expect(screen.getByRole("status").textContent).toBe("Hearth server Not ready");
  expect(fetcher.mock.calls.map(([url]) => url)).toEqual(["/readyz", "http://new-server/readyz"]);
});
