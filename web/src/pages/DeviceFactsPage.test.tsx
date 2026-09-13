import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  installCollectionFetch,
  pageCursorForOffset,
  pagedCollection,
} from "../api/collection-fetch-fake.ts";
import type { DeviceFact, DeviceFactSnapshot, ObservationFactData } from "../api/device-facts.ts";
import type { Availability, Device, Entity } from "../api/types.ts";
import DeviceFactsPage from "./DeviceFactsPage.tsx";

/**
 * Regression coverage for the reported live case: 68 entities and 68 devices,
 * which the deployment served 50 per page, so the Ecowitt entity and the device
 * it belongs to are only on page 2. A one-page read resolves neither name and
 * renders "(unresolved entity)" -- exactly what these tests reject.
 */
const PAGE_SIZE = 50;
const COLLECTION_SIZE = 68;
/** Index of the target entity and device: the first row of page 2. */
const TARGET_INDEX = PAGE_SIZE;
const SEEN_AT = "2026-02-01T10:15:30.000Z";

/**
 * Names and the retained snapshot are literals inside `vi.hoisted` because
 * `vi.mock` factories run before the test module body, so they cannot read
 * module-level bindings declared below them.
 */
const fixtures = vi.hoisted(() => {
  const targetEntityId = "ent_ecowitt_weather_station_outdoor_temperature";
  const targetEntityName = "Ecowitt outdoor temperature";
  const targetDeviceId = "dev_ecowitt_weather_station";
  const targetDeviceName = "Ecowitt weather station";
  const factId = "fct_ecowitt_outdoor_temperature_applied";
  const observationId = "obs_ecowitt_outdoor_temperature_1";
  const data: ObservationFactData = {
    observation_id: observationId,
    entity_id: targetEntityId,
    disposition: "applied",
    value: 21500,
    adapter_received_at: "2026-02-01T10:15:29.900Z",
    observed_at: "2026-02-01T10:15:29.500Z",
  };
  const snapshot: DeviceFactSnapshot = {
    facts: [
      {
        id: factId,
        family: "observation",
        variant: "applied",
        subject: `hearth.v1.core.fact.entity.${targetEntityId}.observation.applied`,
        entityId: targetEntityId,
        envelope: {
          id: factId,
          schema: "urn:hearth:schema:observation-fact:v1",
          emitted_at: "2026-02-01T10:15:30.000Z",
          correlation_id: "cor_ecowitt_weather_station",
          causation_id: observationId,
          data,
        },
        data,
        rawPayload: {
          id: factId,
          schema: "urn:hearth:schema:observation-fact:v1",
          emitted_at: "2026-02-01T10:15:30.000Z",
          correlation_id: "cor_ecowitt_weather_station",
          causation_id: observationId,
          data,
        },
        streamSequence: 12,
        brokerStoredAt: "2026-02-01T10:15:30.050Z",
        consumer: "hearth-device-facts-snapshot-test",
        deliveryCount: 1,
        deliveredBy: "snapshot",
        deliveredAt: "2026-02-01T10:15:31.000Z",
        headers: { "Nats-Msg-Id": [factId] },
        msgIdHeader: factId,
        warnings: [],
      } satisfies DeviceFact,
    ],
    consumer: "hearth-device-facts-snapshot-test",
    streamMessages: 1,
    streamFirstSequence: 12,
    streamLastSequence: 12,
    readAt: "2026-02-01T10:15:31.000Z",
    malformed: 0,
  };
  return { targetEntityId, targetEntityName, targetDeviceId, targetDeviceName, snapshot };
});

// The snapshot transport is mocked at its module seam: these tests exercise
// pagination and rendering, not the broker.
vi.mock("../api/nats.ts", () => ({
  acquireNatsConnection: vi.fn(async () => ({
    connection: {},
    release: async () => {},
  })),
}));

vi.mock("../api/device-facts.ts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/device-facts.ts")>();
  return {
    ...actual,
    readDeviceFactSnapshot: vi.fn(async () => fixtures.snapshot),
  };
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** Collection reads the fake served for one path, in order. */
function requestsForPath(requests: string[], path: string): string[] {
  return requests.filter((url) => url.startsWith(`${path}?`));
}

function available(source: string): Availability {
  return { status: "available", source, since: SEEN_AT, evidence_at: SEEN_AT };
}

/** Filler rows are Zigbee so the target is the only Ecowitt row. */
function fillerEntity(index: number): Entity {
  const suffix = String(index).padStart(3, "0");
  return {
    id: `ent_zigbee_filler_${suffix}`,
    device_id: `dev_zigbee_filler_${suffix}`,
    adapter_id: "adp_zigbee2mqtt",
    name: `Zigbee filler entity ${suffix}`,
    type: "hearth.binarysensor/v1",
    support: {},
    enabled: true,
    availability: available("adp_zigbee2mqtt"),
    state: null,
  };
}

function buildEntityFixtures(): Entity[] {
  const entities = Array.from({ length: COLLECTION_SIZE }, (_, index) => fillerEntity(index));
  entities[TARGET_INDEX] = {
    id: fixtures.targetEntityId,
    device_id: fixtures.targetDeviceId,
    adapter_id: "adp_ecowitt",
    name: fixtures.targetEntityName,
    type: "hearth.temperature/v1",
    support: {},
    enabled: true,
    availability: available("adp_ecowitt"),
    state: null,
  };
  return entities;
}

function buildDeviceFixtures(): Device[] {
  const devices = Array.from({ length: COLLECTION_SIZE }, (_, index) => {
    const suffix = String(index).padStart(3, "0");
    return {
      id: `dev_zigbee_filler_${suffix}`,
      kind: "zigbee",
      name: `Zigbee filler device ${suffix}`,
    };
  });
  devices[TARGET_INDEX] = {
    id: fixtures.targetDeviceId,
    kind: "ecowitt",
    name: fixtures.targetDeviceName,
  };
  return devices;
}

describe("DeviceFactsPage entity and device enrichment", () => {
  it("names an entity and device that only appear on a later collection page", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": pagedCollection(buildEntityFixtures(), PAGE_SIZE),
      "/v1/devices": pagedCollection(buildDeviceFixtures(), PAGE_SIZE),
    });

    render(<DeviceFactsPage />);

    const entityCell = await screen.findByText(fixtures.targetEntityName);
    const factRow = entityCell.closest("tr");
    if (!factRow) throw new Error(`the fact row for ${fixtures.targetEntityId} did not render`);

    expect(factRow.textContent).toContain(fixtures.targetDeviceName);
    expect(factRow.textContent).not.toContain("(unresolved entity)");
    // The page-2 entity type drove the rendered value, not just the name.
    expect(factRow.textContent).toContain("21.50 °C");

    const grid = screen.getByRole("grid", { name: "Device facts, newest first" });
    expect(within(grid).queryByText("(unresolved entity)")).toBeNull();

    const secondPageCursor = encodeURIComponent(pageCursorForOffset(PAGE_SIZE));
    expect(requestsForPath(requests, "/v1/entities")).toEqual([
      "/v1/entities?limit=200",
      `/v1/entities?limit=200&cursor=${secondPageCursor}`,
    ]);
    expect(requestsForPath(requests, "/v1/devices")).toEqual([
      "/v1/devices?limit=200",
      `/v1/devices?limit=200&cursor=${secondPageCursor}`,
    ]);
  });

  it("shows an unresolved row and a failed-enrichment note when a cursor repeats", async () => {
    const { requests } = installCollectionFetch({
      "/v1/entities": () => ({ items: [], next_cursor: pageCursorForOffset(PAGE_SIZE) }),
      "/v1/devices": pagedCollection(buildDeviceFixtures(), PAGE_SIZE),
    });

    render(<DeviceFactsPage />);

    // No partial map: the repeated cursor fails the read, so the id stays raw.
    expect(await screen.findByText(/Enrichment is unavailable right now/)).not.toBeNull();
    expect(screen.getByText("(unresolved entity)")).not.toBeNull();
    // Two reads only: the repeat is rejected, not followed forever.
    expect(requestsForPath(requests, "/v1/entities")).toHaveLength(2);
  });
});
