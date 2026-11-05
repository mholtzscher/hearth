// ucum-lhc is CommonJS marked __esModule: only named imports resolve in Vite dev, Rollup and Vitest alike.
import { UcumLhcUtils } from "@lhncbc/ucum-lhc";
import { precisionOverrides, presentationOverrides, unitSystems, type UnitSystem } from "./systems";

export type { UnitSystem } from "./systems";
export type ValueType = "integer" | "number";
export type DisplayUnit = { label: string; fraction_digits: number; convert: (value: number) => number };
export type Measurement = { value: number; label: string; fraction_digits: number };

const ucum = UcumLhcUtils.getInstance();
const identity = (value: number) => value;
const cache = new Map<string, DisplayUnit>();

function symbol(unit: string): string {
  const specified = ucum.getSpecifiedUnit(unit, "validate").unit;
  return specified?.printSymbol_ || unit;
}

function converter(from: string, to: string): ((value: number) => number) | null {
  const at = (value: number) => {
    const result = ucum.convertUnitTo(from, value, to);
    return result.status === "succeeded" && typeof result.toVal === "number" ? result.toVal : null;
  };
  const zero = at(0), one = at(1), two = at(2);
  if (zero === null || one === null || two === null) return null;
  const slope = one - zero;
  // Current cards also convert min/max/sample means. Only positive affine
  // conversions preserve all three; leave other representations canonical.
  const affine = Math.abs(two - (2 * slope + zero)) <= 1e-9 * Math.max(1, Math.abs(two));
  return affine && Number.isFinite(slope) && slope > 0 ? value => value * slope + zero : null;
}

/** Chooses how a canonical unit is shown under the household system. Never throws. */
export function displayUnit(canonicalUnit: string, valueType: ValueType, system: UnitSystem): DisplayUnit {
  const key = `${system}\u0000${valueType}\u0000${canonicalUnit}`;
  const cached = cache.get(key);
  if (cached) return cached;
  const native = { fraction_digits: valueType === "integer" ? 0 : 1, convert: identity };
  let result: DisplayUnit = { ...native, label: canonicalUnit };
  if (ucum.validateUnitString(canonicalUnit).status === "valid") {
    result = { ...native, label: symbol(canonicalUnit) };
    for (const preferred of unitSystems[system]) {
      const convert = converter(canonicalUnit, preferred);
      if (convert) {
        result = { label: symbol(preferred), fraction_digits: precisionOverrides[preferred] ?? 1, convert };
        break;
      }
    }
    const override = presentationOverrides[canonicalUnit];
    if (override) {
      const convert = override.to ? converter(canonicalUnit, override.to) : identity;
      if (convert) result = { label: override.label, fraction_digits: override.fraction_digits ?? native.fraction_digits, convert };
    }
  }
  cache.set(key, result);
  return result;
}

/** Converts one canonical value for display under the household system. */
export function present(value: number, canonicalUnit: string, valueType: ValueType, system: UnitSystem): Measurement {
  const unit = displayUnit(canonicalUnit, valueType, system);
  return { value: unit.convert(value), label: unit.label, fraction_digits: unit.fraction_digits };
}
