import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { NotificationAttempt } from "../api/notifications.ts";
import NotificationDestinationDetailPage from "./notification-destination-detail-page.tsx";
import NotificationDestinationsPage from "./notification-destinations-page.tsx";
import NotificationsPage from "./notifications-page.tsx";

type FetchCall = [url: string, init: RequestInit];
type FetchMock = (...args: FetchCall) => Promise<Response>;
const destination = {
  id: "ndst_household",
  name: "Household alerts",
  available: true,
};
const attempt: NotificationAttempt = {
  id: "ntf_test",
  destination_id: destination.id,
  destination_name: "Original destination name",
  title: "Freezer",
  message: "Too warm",
  status: "accepted",
  started_at: "2026-10-11T10:00:00Z",
  deadline: "2026-10-11T10:00:10Z",
  completed_at: "2026-10-11T10:00:01Z",
  receipt: "provider-123",
};
const respond = (body: unknown, status = 200) =>
  Promise.resolve(
    Response.json(body, {
      status,
      headers: { "content-type": "application/json" },
    })
  );
const mount = (path: string) => {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/notification-destinations"
          element={<NotificationDestinationsPage />}
        />
        <Route
          path="/notification-destinations/:destinationId"
          element={<NotificationDestinationDetailPage />}
        />
        <Route path="/notifications" element={<NotificationsPage />} />
        <Route
          path="/notifications/:attemptId"
          element={<NotificationsPage />}
        />
      </Routes>
    </MemoryRouter>
  );
};

describe(NotificationsPage, () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it("discovers paged destinations, sends once while pending, and opens its independent history detail", async () => {
    const { promise, resolve: finish } = Promise.withResolvers<Response>();
    const fetcher = vi.fn<FetchMock>((url, init) => {
      if (init?.method === "POST") {
        return promise;
      }
      if (url === "/v1/notification-destinations?limit=50") {
        return respond({ items: [], next_cursor: "page/1+=" });
      }
      if (url.includes("notification-destinations?")) {
        return respond({ items: [destination], next_cursor: null });
      }
      if (url === `/v1/notification-destinations/${destination.id}`) {
        return respond(destination);
      }
      if (url === `/v1/notifications/${attempt.id}`) {
        return respond(attempt);
      }
      if (url === "/v1/notifications?limit=50") {
        return respond({ items: [attempt], next_cursor: null });
      }
      throw new Error(`Unexpected read ${url}`);
    });
    vi.stubGlobal("fetch", fetcher);
    mount("/notification-destinations");
    fireEvent.click(await screen.findByRole("button", { name: "Next page" }));
    fireEvent.click(
      await screen.findByRole("link", { name: destination.name })
    );
    await screen.findByLabelText("Message (required, up to 2000 characters)");
    fireEvent.change(
      screen.getByLabelText("Title (optional, up to 256 characters)"),
      { target: { value: "Freezer" } }
    );
    fireEvent.change(
      screen.getByLabelText("Message (required, up to 2000 characters)"),
      { target: { value: "Too warm" } }
    );
    fireEvent.click(screen.getByRole("button", { name: "Send test message" }));
    expect(
      (screen.getByRole("button", { name: "Sending…" }) as HTMLButtonElement)
        .disabled
    ).toBeTruthy();
    const form = screen
      .getByLabelText("Message (required, up to 2000 characters)")
      .closest("form");
    if (!form) {
      throw new Error("Expected the notification form");
    }
    fireEvent.submit(form);
    finish(respond(attempt, 201));
    fireEvent.click(
      await screen.findByRole("link", { name: `Inspect attempt ${attempt.id}` })
    );
    await screen.findByRole("heading", { name: `Attempt ${attempt.id}` });
    await screen.findByText("provider-123");
    expect(screen.getByText("Too warm").textContent).toBe("Too warm");
    expect(
      screen.getByText(
        "Provider confirmed acceptance, not delivery to a recipient."
      )
    ).not.toBeNull();
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST")
    ).toHaveLength(1);
    const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST");
    expect(post?.[0]).toBe(
      `/v1/notification-destinations/${destination.id}/notifications`
    );
  });

  it("posts the entered title and message to the destination", async () => {
    const fetcher = vi.fn<FetchMock>((_url, init) =>
      init?.method === "POST" ? respond(attempt, 201) : respond(destination)
    );
    vi.stubGlobal("fetch", fetcher);
    mount(`/notification-destinations/${destination.id}`);
    fireEvent.change(
      await screen.findByLabelText("Message (required, up to 2000 characters)"),
      { target: { value: "Too warm" } }
    );
    fireEvent.change(
      screen.getByLabelText("Title (optional, up to 256 characters)"),
      { target: { value: "Freezer" } }
    );
    fireEvent.click(screen.getByRole("button", { name: "Send test message" }));
    await screen.findByRole("link", { name: `Inspect attempt ${attempt.id}` });
    const post = fetcher.mock.calls.find(([, init]) => init?.method === "POST");
    if (!post) {
      throw new Error("Expected a POST request");
    }
    expect(JSON.parse(String(post[1].body))).toStrictEqual({
      title: "Freezer",
      message: "Too warm",
    });
  });

  it("requests the next destination page with the returned cursor", async () => {
    const fetcher = vi.fn<FetchMock>((url) => {
      if (url === "/v1/notification-destinations?limit=50") {
        return respond({ items: [], next_cursor: "page/1+=" });
      }
      return respond({ items: [destination], next_cursor: null });
    });
    vi.stubGlobal("fetch", fetcher);
    mount("/notification-destinations");
    fireEvent.click(await screen.findByRole("button", { name: "Next page" }));
    await screen.findByRole("link", { name: destination.name });
    expect(
      fetcher.mock.calls.some(
        ([url]) =>
          url === "/v1/notification-destinations?limit=50&cursor=page%2F1%2B%3D"
      )
    ).toBeTruthy();
  });

  it("does not retry an ambiguous HTTP failure on refresh or history navigation", async () => {
    const fetcher = vi.fn<FetchMock>((url, init) => {
      if (init?.method === "POST") {
        throw new TypeError("Connection lost");
      }
      if (url.startsWith("/v1/notification-destinations/")) {
        return respond(destination);
      }
      return respond({
        items: [
          { ...attempt, status: "unknown", reason_code: "outcome_timeout" },
        ],
        next_cursor: null,
      });
    });
    vi.stubGlobal("fetch", fetcher);
    mount(`/notification-destinations/${destination.id}`);
    fireEvent.change(
      await screen.findByLabelText("Message (required, up to 2000 characters)"),
      { target: { value: "Test" } }
    );
    fireEvent.click(screen.getByRole("button", { name: "Send test message" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Connection lost");
    expect(screen.getByRole("status").textContent).toContain(
      "No retry was made"
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh destination" })
    );
    fireEvent.click(
      screen.getByRole("link", { name: "History for this destination" })
    );
    await screen.findByRole("link", { name: attempt.id });
    fireEvent.click(screen.getByRole("button", { name: "Refresh history" }));
    await waitFor(() =>
      expect(
        fetcher.mock.calls.filter(([url]) =>
          url.startsWith("/v1/notifications?")
        ).length
      ).toBeGreaterThan(1)
    );
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST")
    ).toHaveLength(1);
  });

  it("keeps all five statuses visible and resets destination-bound cursors when filtering", async () => {
    const statuses: NotificationAttempt["status"][] = [
      "running",
      "accepted",
      "rejected",
      "not_sent",
      "unknown",
    ];
    const fetcher = vi.fn<FetchMock>((url) =>
      respond({
        items: url.includes("cursor=")
          ? [{ ...attempt, id: "ntf_older" }]
          : statuses.map((status) => ({
              ...attempt,
              id: `ntf_${status}`,
              status,
              reason_code: status === "unknown" ? "outcome_timeout" : undefined,
            })),
        next_cursor:
          url.includes("cursor=") || url.includes("destination_id=")
            ? null
            : "older/+=",
      })
    );
    vi.stubGlobal("fetch", fetcher);
    mount("/notifications");
    const table = await screen.findByRole("table");
    await screen.findByRole("link", { name: "ntf_unknown" });
    for (const status of statuses) {
      expect(within(table).getByText(status)).not.toBeNull();
    }
    expect(
      screen.getAllByText("Original destination name", { selector: "td" })
    ).toHaveLength(5);
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByRole("link", { name: "ntf_older" });
    fireEvent.change(screen.getByLabelText("Destination ID filter"), {
      target: { value: "ndst_other" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply filter" }));
    await screen.findByRole("link", { name: "ntf_unknown" });
    expect(fetcher.mock.calls.map(([url]) => url)).toContain(
      "/v1/notifications?limit=50&cursor=older%2F%2B%3D"
    );
    expect(fetcher.mock.calls.at(-1)?.[0]).toBe(
      "/v1/notifications?limit=50&destination_id=ndst_other"
    );
    fireEvent.click(screen.getByRole("button", { name: "Clear filter" }));
    await waitFor(() =>
      expect(fetcher.mock.calls.at(-1)?.[0]).toBe("/v1/notifications?limit=50")
    );
  });

  it("explains pruned detail while retaining notification history", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<FetchMock>((url) =>
        url === "/v1/notifications/ntf_pruned"
          ? respond({ title: "Not Found" }, 404)
          : respond({
              items: [
                {
                  ...attempt,
                  status: "rejected",
                  reason_code: "destination_unknown",
                },
              ],
              next_cursor: null,
            })
      )
    );
    mount("/notifications/ntf_pruned");
    const status = await screen.findByRole("status");
    expect(status.textContent).toContain(
      "Any retained Automation outcome is unchanged"
    );
    expect(screen.queryByRole("alert")).toBeNull();
    await screen.findByRole("link", { name: attempt.id });
    expect(screen.getByText("destination_unknown")).not.toBeNull();
  });

  it("records a visible not-sent result for an unavailable destination rather than treating 201 as acceptance", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn<FetchMock>((_url, init) =>
        init?.method === "POST"
          ? respond(
              {
                ...attempt,
                status: "not_sent",
                reason_code: "destination_unavailable",
                receipt: undefined,
              },
              201
            )
          : respond({ ...destination, available: false })
      )
    );
    mount(`/notification-destinations/${destination.id}`);
    fireEvent.change(
      await screen.findByLabelText("Message (required, up to 2000 characters)"),
      { target: { value: "Test" } }
    );
    fireEvent.click(screen.getByRole("button", { name: "Send test message" }));
    const result = await screen.findByRole("status");
    expect(result.textContent).toContain("not_sent");
    expect(result.textContent).toContain("destination_unavailable");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("prevents blank and oversized messages but counts supplementary Unicode as single code points", async () => {
    const fetcher = vi.fn<FetchMock>((_url, init) =>
      init?.method === "POST" ? respond(attempt, 201) : respond(destination)
    );
    vi.stubGlobal("fetch", fetcher);
    mount(`/notification-destinations/${destination.id}`);
    const input = await screen.findByLabelText(
      "Message (required, up to 2000 characters)"
    );
    const send = screen.getByRole("button", {
      name: "Send test message",
    }) as HTMLButtonElement;
    for (const value of ["   ", "a".repeat(2001)]) {
      fireEvent.change(input, { target: { value } });
      expect(send.disabled).toBeTruthy();
    }
    fireEvent.change(input, { target: { value: "😀".repeat(2000) } });
    expect(send.disabled).toBeFalsy();
    fireEvent.change(
      screen.getByLabelText("Title (optional, up to 256 characters)"),
      { target: { value: "a".repeat(257) } }
    );
    expect(send.disabled).toBeTruthy();
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST")
    ).toHaveLength(0);
  });
});
