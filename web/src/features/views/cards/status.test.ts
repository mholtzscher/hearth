import { describe, expect, it } from "vitest";
import type { Resolved } from "../bindings/types";
import { cardStatus } from "./status";

const metadata = (availability: string, enabled = true) => ({ id: "ent_1", label: "Lamp", enabled, availability });
const reading = { value: 1 };

describe("cardStatus follows the View cards state table", () => {
  const rows: Array<[string, Resolved<unknown>, string[], string, boolean]> = [
    ["loading", { status: "loading" }, ["loading:neutral"], "empty", false],
    ["missing", { status: "missing", metadata: metadata("available"), refreshFailed: true }, ["missing:error"], "empty", false],
    ["request error", { status: "error", error: "HTTP 500" }, ["error:error"], "empty", false],
    ["unavailable", { status: "ready", data: reading, metadata: metadata("unavailable") }, ["unavailable:warn"], "dimmed", false],
    ["disabled", { status: "ready", data: reading, metadata: metadata("available", false) }, ["disabled:neutral"], "dimmed", false],
    ["refresh failed", { status: "ready", data: reading, metadata: metadata("available"), refreshFailed: true }, ["refresh-failed:warn"], "live", true],
    ["availability unknown", { status: "ready", data: reading, metadata: metadata("unknown") }, ["availability-unknown:neutral"], "live", false],
    ["never observed", { status: "unobserved", metadata: metadata("available"), unobserved: true }, ["unobserved:neutral"], "empty", false],
    ["absent", { status: "absent", metadata: metadata("available") }, ["absent:neutral"], "empty", false],
    ["ready and available", { status: "ready", data: reading, metadata: metadata("available") }, [], "live", true],
  ];
  it.each(rows)("%s", (_, resource, entries, value, glows) => {
    const status = cardStatus(resource);
    expect(status.entries.map(entry => `${entry.key}:${entry.tone}`)).toEqual(entries);
    expect(status.value).toBe(value);
    expect(status.glows).toBe(glows);
  });

  it("keeps the request error as the entry's detail", () => {
    expect(cardStatus({ status: "error", error: "HTTP 500" }).entries[0]).toMatchObject({ text: "Couldn't load this reading", detail: "HTTP 500" });
  });

  it("lists combined states in precedence order", () => {
    const status = cardStatus({ status: "ready", data: reading, metadata: metadata("unavailable", false), refreshFailed: true });
    expect(status.entries.map(entry => entry.key)).toEqual(["unavailable", "disabled", "refresh-failed"]);
    expect(status.value).toBe("dimmed");
    expect(status.glows).toBe(false);
  });
});
