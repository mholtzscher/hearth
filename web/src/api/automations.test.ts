import { afterEach, expect, it, vi } from "vitest";
import { createAutomation, replaceAutomation } from "./automations";
import { readAutomationDocument } from "../features/automations/document";

afterEach(() => vi.unstubAllGlobals());
it("uses a raw definition for POST and the revision envelope only for PUT", async () => {
  const document = readAutomationDocument('{"name":"Exact","enabled":false,"variables":{"n":9007199254740993},"triggers":[],"steps":[{"id":"d","kind":"delay","duration_ms":1}]}');
  const requests: { path: string; init: RequestInit }[] = [];
  vi.stubGlobal("fetch", async (path: string, init: RequestInit) => { requests.push({ path, init }); return new Response(`{"id":"aut_example","revision":4,"definition":${document.source}}`); });
  await createAutomation(document); await replaceAutomation("aut_example", 3, document);
  expect(requests.map(item => [item.init.method, item.init.body])).toEqual([["POST", document.source], ["PUT", `{"expected_revision":3,"definition":${document.source}}`]]);
});
