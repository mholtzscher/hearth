import { createContext, useContext, useEffect, useState } from "react";
import { z } from "zod";
import { apiFetch, useBaseUrlVersion } from "../../api/client";
import type { UnitSystem } from "./systems";

const householdSchema = z.object({ timezone: z.string(), unit_system: z.enum(["metric", "us"]) });
const loads = new Map<number, Promise<UnitSystem>>();

function load(version: number): Promise<UnitSystem> {
  let pending = loads.get(version);
  if (!pending) {
    pending = apiFetch("/v1/household", undefined, text => householdSchema.parse(JSON.parse(text)))
      .then(body => body.unit_system, (): UnitSystem => "metric");
    loads.set(version, pending);
  }
  return pending;
}

/** Supplies a fixed unit system instead of reading the household endpoint (tests, embeds). */
export const UnitSystemOverride = createContext<UnitSystem | undefined>(undefined);

/**
 * Reads the household unit system once per API base URL. Returns undefined
 * until it resolves, so numeric cards can stay in their loading state rather
 * than flash the wrong unit; a failed read falls back to metric.
 */
export function useUnitSystem(): UnitSystem | undefined {
  const override = useContext(UnitSystemOverride);
  const version = useBaseUrlVersion();
  const [state, setState] = useState<{ version: number; system: UnitSystem } | undefined>();
  useEffect(() => {
    if (override) return;
    let current = true;
    void load(version).then(system => { if (current) setState({ version, system }); });
    return () => { current = false; };
  }, [override, version]);
  if (override) return override;
  return state?.version === version ? state.system : undefined;
}

/** Test seam: forget cached household reads. */
export function resetHouseholdCache(): void { loads.clear(); }
