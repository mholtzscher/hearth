import { vi } from "vitest";
import type { Automation, AutomationRun, AutomationSkip, Entity } from "../api/types.ts";

/**
 * Test-only fake `fetch` for the Automation HTTP API, plus the Automation
 * fixtures the page tests share.
 *
 * Every dashboard read goes through `apiFetch`, which calls the global `fetch`,
 * so stubbing that global is the seam that lets a test serve Automation
 * responses without a server. Routes are matched in the order given and the
 * first match answers, so a test puts its overrides before the defaults.
 *
 * A request matching no route fails the test with its method and path instead
 * of silently answering: an unexpected request (or a missing route for one) is
 * reported as a failure rather than hidden behind an empty response.
 */

/** One request the fake served, with its body already parsed. */
export interface AutomationRequest {
  method: string;
  /** Pathname only, e.g. `/v1/automations`. */
  path: string;
  /** Full path and query as sent, so cursor encoding mistakes stay visible. */
  url: string;
  /** Parsed JSON request body, or null when the request carried none. */
  body: unknown;
}

/** One fake response: an HTTP status plus a JSON body. */
export interface AutomationResponse {
  status?: number;
  body?: unknown;
}

export interface AutomationRoute {
  method: string;
  path: string | RegExp;
  respond: (request: AutomationRequest) => AutomationResponse;
}

const FAKE_ORIGIN = "http://hearthd.test";

/** Install a fake `fetch` answering `routes` in order, and return the recorded
    requests. */
export function installAutomationFetch(routes: AutomationRoute[]): AutomationRequest[] {
  const requests: AutomationRequest[] = [];
  vi.stubGlobal(
    "fetch",
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const url = String(input);
      const parsed = new URL(url, FAKE_ORIGIN);
      const method = (init?.method ?? "GET").toUpperCase();
      const request: AutomationRequest = {
        method,
        path: parsed.pathname,
        url,
        body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
      };
      requests.push(request);
      // Yield to the macrotask queue like a real round trip does.
      await new Promise((resolve) => setTimeout(resolve, 0));
      const route = routes.find(
        (candidate) => candidate.method === method && routeMatches(candidate.path, parsed.pathname),
      );
      if (!route) {
        throw new Error(`automation fetch fake has no route for ${method} ${parsed.pathname}`);
      }
      const response = route.respond(request);
      return new Response(response.body === undefined ? "" : JSON.stringify(response.body), {
        status: response.status ?? 200,
        headers: { "content-type": "application/json" },
      });
    },
  );
  return requests;
}

function routeMatches(path: string | RegExp, pathname: string): boolean {
  return typeof path === "string" ? path === pathname : path.test(pathname);
}

/** One hearthd RFC 9457 problem response with its stable `code`. */
export function problemResponse(status: number, code: string, detail: string): AutomationResponse {
  return { status, body: { type: "about:blank", title: String(status), status, detail, code } };
}

/** Requests the fake recorded for one method and pathname prefix. */
export function requestsFor(
  requests: AutomationRequest[],
  method: string,
  path: string,
): AutomationRequest[] {
  return requests.filter(
    (request) => request.method === method && request.path === path,
  );
}

export const AUTOMATION_ID = "aut_01920000-0000-7000-8000-000000000001";
export const RUN_ID = "arn_01920000-0000-7000-8000-000000000002";
export const SKIP_ID = "ask_01920000-0000-7000-8000-000000000003";
export const ENTITY_ID = "ent_01920000-0000-7000-8000-000000000004";
export const ENTITY_NAME = "Kitchen button";
export const VERIFIED_COMMAND_ID = "cmd_01920000-0000-7000-8000-000000000005";
export const AUTOMATION_NAME = "Kitchen light on press";
export const AUTOMATION_REVISION = 3;

/** One Automation: an Entity Event Trigger on the kitchen button, one Step that
    turns the light on. */
export function automationFixture(overrides: Partial<Automation> = {}): Automation {
  return {
    id: AUTOMATION_ID,
    revision: AUTOMATION_REVISION,
    created_at: "2026-02-01T09:00:00.000Z",
    updated_at: "2026-02-01T09:30:00.000Z",
    definition: {
      name: AUTOMATION_NAME,
      enabled: true,
      triggers: [
        {
          id: "button_press",
          kind: "entity_event",
          entity_id: ENTITY_ID,
          event_name: "single_press",
        },
      ],
      steps: [
        {
          id: "turn_on",
          entity_id: ENTITY_ID,
          operation: "set",
          parameters: { value: true },
        },
      ],
    },
    ...overrides,
  };
}

/** One Run: a manual Run that satisfied its single Step. */
export function runFixture(overrides: Partial<AutomationRun> = {}): AutomationRun {
  return {
    id: RUN_ID,
    automation_id: AUTOMATION_ID,
    automation_name: AUTOMATION_NAME,
    revision: AUTOMATION_REVISION,
    source: "manual",
    matched_trigger_ids: [],
    status: "succeeded",
    started_at: "2026-02-01T10:00:00.000Z",
    completed_at: "2026-02-01T10:00:01.000Z",
    snapshot: automationFixture().definition,
    steps: [
      {
        position: 0,
        step_id: "turn_on",
        status: "satisfied",
        verified_command_id: VERIFIED_COMMAND_ID,
        started_at: "2026-02-01T10:00:00.100Z",
        completed_at: "2026-02-01T10:00:00.900Z",
      },
    ],
    ...overrides,
  };
}

/** One Skip: a matched Fact that arrived while the Automation was busy. */
export function skipFixture(overrides: Partial<AutomationSkip> = {}): AutomationSkip {
  return {
    id: SKIP_ID,
    automation_id: AUTOMATION_ID,
    automation_name: AUTOMATION_NAME,
    revision: AUTOMATION_REVISION,
    fact: {
      fact_id: "fct_01920000-0000-7000-8000-000000000006",
      family: "entity_event",
      entity_id: ENTITY_ID,
      variant: "single_press",
      causation_id: "evt_01920000-0000-7000-8000-000000000007",
      emitted_at: "2026-02-01T10:05:00.000Z",
    },
    matched_triggers: automationFixture().definition.triggers,
    reason: "automation_busy",
    skipped_at: "2026-02-01T10:05:00.050Z",
    ...overrides,
  };
}

/** The Entities read the detail page uses to label Trigger and Step references. */
export function entityFixture(overrides: Partial<Entity> = {}): Entity {
  return {
    id: ENTITY_ID,
    device_id: "dev_01920000-0000-7000-8000-000000000008",
    adapter_id: "adp_zigbee2mqtt",
    name: ENTITY_NAME,
    type: "hearth.enumaction/v1",
    support: { operations: { trigger: { values: ["single_press"] } } },
    enabled: true,
    availability: {
      status: "available",
      source: "adp_zigbee2mqtt",
      since: "2026-02-01T09:00:00.000Z",
      evidence_at: "2026-02-01T09:00:00.000Z",
    },
    state: null,
    ...overrides,
  };
}
