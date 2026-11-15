import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { pairedEntityResponse } from "../../../.storybook/paired-fixtures";
import { setBaseUrl } from "../../api/client.ts";
import DevicesPage from "../../pages/devices-page.tsx";
import EntityDetailPage from "../../pages/entity-detail-page.tsx";
import NameEditor from "./name-editor.tsx";

type FetchInit = RequestInit & { headers: Record<string, string> };
type FetchCall = [url: string, init: FetchInit];
type FetchMock = (...args: FetchCall) => Promise<Response>;
const naming = {
  name: "Adapter lamp",
  adapter_name: "Adapter lamp",
  name_override: null,
};
const response = (body: unknown, status = 200) =>
  Response.json(body, { status });
const rename = (value: string) => {
  fireEvent.click(screen.getByRole("button", { name: "Rename" }));
  fireEvent.change(screen.getByLabelText("Display name"), {
    target: { value },
  });
};

describe(NameEditor, () => {
  afterEach(() => {
    cleanup();
    setBaseUrl("");
    vi.unstubAllGlobals();
  });

  it("cancels without HTTP and retains a rejected draft across refresh", async () => {
    const fetch = vi
      .fn<FetchMock>()
      .mockResolvedValueOnce(response({ detail: "Invalid name" }, 400));
    vi.stubGlobal("fetch", fetch);
    const updated = vi.fn<Parameters<typeof NameEditor>[0]["onUpdated"]>();
    const view = render(
      <NameEditor
        objectId="dev_a"
        kind="device"
        metadata={naming}
        onUpdated={updated}
      />
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Reset to Adapter name",
        }) as HTMLButtonElement
      ).disabled
    ).toBeTruthy();
    rename("Cancelled");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(fetch).not.toHaveBeenCalled();
    rename("😀".repeat(128));
    expect(screen.getByText(/128 code points\./u)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("Invalid name");
    view.rerender(
      <NameEditor
        objectId="dev_a"
        kind="device"
        metadata={{ ...naming, name: "Refreshed" }}
        onUpdated={updated}
      />
    );
    expect(
      (screen.getByLabelText("Display name") as HTMLInputElement).value
    ).toBe("😀".repeat(128));
  });

  it("retries a rejected draft and saves it", async () => {
    const fetch = vi
      .fn<FetchMock>()
      .mockResolvedValueOnce(response({ detail: "Invalid name" }, 400))
      .mockResolvedValueOnce(
        response({
          ...naming,
          name: "😀".repeat(128),
          name_override: "😀".repeat(128),
        })
      );
    vi.stubGlobal("fetch", fetch);
    const updated = vi.fn<Parameters<typeof NameEditor>[0]["onUpdated"]>();
    render(
      <NameEditor
        objectId="dev_a"
        kind="device"
        metadata={naming}
        onUpdated={updated}
      />
    );
    rename("😀".repeat(128));
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("Invalid name");
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    await waitFor(() => expect(updated).toHaveBeenCalledOnce());
    expect(fetch.mock.calls[1][0]).toBe("/v1/devices/dev_a");
    expect(JSON.parse(fetch.mock.calls[1][1].body as string)).toStrictEqual({
      name_override: "😀".repeat(128),
    });
    expect(fetch.mock.calls[1][1].headers["content-type"]).toBe(
      "application/merge-patch+json"
    );
  });

  it("resets to the Adapter name", async () => {
    const fetch = vi.fn<FetchMock>().mockResolvedValueOnce(
      response({
        ...naming,
        name: "Latest Adapter",
        adapter_name: "Latest Adapter",
      })
    );
    vi.stubGlobal("fetch", fetch);
    const updated = vi.fn<Parameters<typeof NameEditor>[0]["onUpdated"]>();
    render(
      <NameEditor
        objectId="dev_a"
        kind="device"
        metadata={{ ...naming, name_override: "Pinned" }}
        onUpdated={updated}
      />
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Reset to Adapter name" })
    );
    await waitFor(() => expect(updated).toHaveBeenCalledOnce());
    expect(JSON.parse(fetch.mock.calls[0][1].body as string)).toStrictEqual({
      name_override: null,
    });
    expect(fetch.mock.calls[0][1].headers["content-type"]).toBe(
      "application/merge-patch+json"
    );
    expect(updated.mock.calls[0][0].name).toBe("Latest Adapter");
  });

  it.each(
    ["id", "kind", "server"].flatMap((scope) =>
      [200, 500].map((status) => ({ scope, status }))
    )
  )(
    "ignores a pending $status response after switching $scope away and back",
    async ({ scope, status }) => {
      const { promise, resolve } = Promise.withResolvers<Response>();
      vi.stubGlobal(
        "fetch",
        vi.fn<FetchMock>(() => promise)
      );
      const updated = vi.fn<Parameters<typeof NameEditor>[0]["onUpdated"]>();
      const props = {
        objectId: "same",
        kind: "device" as const,
        metadata: naming,
        onUpdated: updated,
      };
      const view = render(<NameEditor {...props} />);
      rename("Old request");
      fireEvent.click(screen.getByRole("button", { name: "Save name" }));
      expect(
        (screen.getByRole("button", { name: "Saving…" }) as HTMLButtonElement)
          .disabled
      ).toBeTruthy();
      if (scope === "server") {
        act(() => setBaseUrl("http://other"));
        act(() => setBaseUrl(""));
      } else {
        view.rerender(
          <NameEditor
            {...props}
            {...(scope === "id"
              ? { objectId: "other" }
              : { kind: "entity" as const })}
          />
        );
        view.rerender(<NameEditor {...props} />);
      }
      rename("New draft");
      await act(() => {
        resolve(
          response(
            {
              ...naming,
              name: "Old request",
              name_override: "Old request",
              detail: "Old failure",
            },
            status
          )
        );
        return Promise.resolve();
      });
      expect(updated).not.toHaveBeenCalled();
      expect(
        (screen.getByLabelText("Display name") as HTMLInputElement).value
      ).toBe("New draft");
      expect(screen.queryByRole("alert")).toBeNull();
    }
  );

  it("Device detail ignores a rename after selecting away and back", async () => {
    const { promise, resolve } = Promise.withResolvers<Response>();
    vi.stubGlobal(
      "fetch",
      vi.fn<FetchMock>((url, init) => {
        if (init?.method === "PATCH") {
          return promise;
        }
        if (url.includes("/v1/devices/")) {
          return Promise.resolve(
            response({
              ...naming,
              id: url.includes("dev_b") ? "dev_b" : "dev_a",
              kind: "light",
              entities: [],
            })
          );
        }
        return Promise.resolve(
          response({
            items: [
              { ...naming, id: "dev_a", kind: "light" },
              { ...naming, name: "Other lamp", id: "dev_b", kind: "light" },
            ],
          })
        );
      })
    );
    render(
      <MemoryRouter>
        <DevicesPage />
      </MemoryRouter>
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Adapter lamp" })
    );
    await screen.findByRole("button", { name: "Rename" });
    rename("Old rename");
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    fireEvent.click(screen.getByRole("button", { name: "Other lamp" }));
    await screen.findByRole("button", { name: "Rename" });
    fireEvent.click(screen.getByRole("button", { name: "Adapter lamp" }));
    await screen.findByRole("button", { name: "Rename" });
    rename("Current draft");
    await act(() => {
      resolve(
        response({ ...naming, name: "Old rename", name_override: "Old rename" })
      );
      return Promise.resolve();
    });
    expect(
      (screen.getByLabelText("Display name") as HTMLInputElement).value
    ).toBe("Current draft");
    expect(screen.queryByText("Display name: Old rename")).toBeNull();
  });

  const entity = {
    ...naming,
    id: "ent_a",
    name: "Power",
    adapter_name: "Power",
    device_id: "dev_a",
    adapter_id: "adp_a",
    type: "hearth.power/v1",
    support: {},
    enabled: false,
    state: null,
    availability: {
      status: "unavailable",
      source: "adapter",
      since: "now",
      evidence_at: "now",
    },
  };

  it("Device detail merges PATCH metadata, preserves Entities while refetching and preserves drafts on refresh", async () => {
    let detailReads = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn<FetchMock>((url, init) => {
        if (init?.method === "PATCH") {
          return Promise.resolve(
            response({
              ...naming,
              id: "dev_a",
              kind: "light",
              name: "Kitchen",
              name_override: "Kitchen",
            })
          );
        }
        if (url.includes("/dev_a?")) {
          detailReads += 1;
          if (detailReads > 2) {
            return Promise.withResolvers<Response>().promise;
          }
          return Promise.resolve(
            response({
              ...naming,
              id: "dev_a",
              kind: "light",
              entities: [entity],
            })
          );
        }
        return Promise.resolve(
          response({ items: [{ ...naming, id: "dev_a", kind: "light" }] })
        );
      })
    );
    render(
      <MemoryRouter>
        <DevicesPage />
      </MemoryRouter>
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Adapter lamp" })
    );
    await screen.findByRole("button", { name: "Rename" });
    rename("Kitchen");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(detailReads).toBe(2));
    expect(
      (screen.getByLabelText("Display name") as HTMLInputElement).value
    ).toBe("Kitchen");
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    await screen.findByText("Display name: Kitchen");
    expect(screen.getByRole("link", { name: "Power" })).toBeTruthy();
    expect(screen.queryByText("No entities on this device.")).toBeNull();
  });

  it("Entity detail updates its heading from PATCH while disabled/offline and refreshes without losing a draft", async () => {
    let reads = 0;
    const fetch = vi.fn<FetchMock>((url, init) => {
      if (init?.method === "PATCH") {
        return Promise.resolve(
          response({
            ...entity,
            name: "Reading room",
            name_override: "Reading room",
          })
        );
      }
      if (url === "/v1/entities/ent_a") {
        reads += 1;
        if (reads > 2) {
          return Promise.withResolvers<Response>().promise;
        }
        return Promise.resolve(response(pairedEntityResponse(entity, entity)));
      }
      return Promise.resolve(response({ items: [] }));
    });
    vi.stubGlobal("fetch", fetch);
    render(
      <MemoryRouter initialEntries={["/entities/ent_a"]}>
        <Routes>
          <Route path="/entities/:entityId" element={<EntityDetailPage />} />
        </Routes>
      </MemoryRouter>
    );
    await screen.findByRole("button", { name: "Rename" });
    rename("Reading room");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(reads).toBe(2));
    expect(
      (screen.getByLabelText("Display name") as HTMLInputElement).value
    ).toBe("Reading room");
    fireEvent.click(screen.getByRole("button", { name: "Save name" }));
    await screen.findByRole("heading", { name: "Reading room" });
    const patch = fetch.mock.calls.find((call) => call[1]?.method === "PATCH");
    if (!patch) {
      throw new Error("Expected a PATCH request");
    }
    expect(patch[0]).toBe("/v1/entities/ent_a");
    expect(JSON.parse(patch[1].body as string)).toStrictEqual({
      name_override: "Reading room",
    });
    expect(new Headers(patch[1].headers).get("content-type")).toBe(
      "application/merge-patch+json"
    );
    expect(
      screen
        .getByRole("switch", { name: "Enabled" })
        .getAttribute("aria-checked")
    ).toBe("false");
  });

  it("Entity enablement uses merge patch without adding a content type to reads", async () => {
    const enabledEntity = { ...entity, enabled: true };
    const fetch = vi.fn<FetchMock>((url, init) => {
      if (init?.method === "PATCH") {
        return Promise.resolve(response({ ...enabledEntity, enabled: false }));
      }
      if (url === "/v1/entities/ent_a") {
        return Promise.resolve(
          response(pairedEntityResponse(enabledEntity, enabledEntity))
        );
      }
      return Promise.resolve(response({ items: [] }));
    });
    vi.stubGlobal("fetch", fetch);
    render(
      <MemoryRouter initialEntries={["/entities/ent_a"]}>
        <Routes>
          <Route path="/entities/:entityId" element={<EntityDetailPage />} />
        </Routes>
      </MemoryRouter>
    );
    fireEvent.click(await screen.findByRole("switch", { name: "Enabled" }));
    await waitFor(() =>
      expect(
        fetch.mock.calls.some((call) => call[1]?.method === "PATCH")
      ).toBeTruthy()
    );
    const patch = fetch.mock.calls.find((call) => call[1]?.method === "PATCH");
    if (!patch) {
      throw new Error("Expected a PATCH request");
    }
    expect(JSON.parse(patch[1].body as string)).toStrictEqual({
      enabled: false,
    });
    expect(new Headers(patch[1].headers).get("content-type")).toBe(
      "application/merge-patch+json"
    );
    const read = fetch.mock.calls.find(
      (call) => call[0] === "/v1/entities/ent_a" && call[1]?.method !== "PATCH"
    );
    if (!read) {
      throw new Error("Expected an Entity read");
    }
    expect(new Headers(read[1].headers).has("content-type")).toBeFalsy();
  });
});
