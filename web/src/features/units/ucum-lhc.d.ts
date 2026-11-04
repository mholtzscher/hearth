// Minimal typings for the parts of @lhncbc/ucum-lhc the units module uses.
declare module "@lhncbc/ucum-lhc" {
  type ConversionResult = { status: "succeeded" | "failed" | "error"; toVal: number | null; msg: string[] };
  type ValidationResult = { status: "valid" | "invalid" | "error"; msg: string[] };
  type SpecifiedUnit = { unit: { printSymbol_?: string | null; csCode_?: string } | null };
  interface UcumLhcUtilsInstance {
    validateUnitString(unit: string, suggest?: boolean): ValidationResult;
    convertUnitTo(fromUnit: string, fromValue: number, toUnit: string): ConversionResult;
    getSpecifiedUnit(unit: string, purpose: "validate" | "convert"): SpecifiedUnit;
  }
  export const UcumLhcUtils: { getInstance(): UcumLhcUtilsInstance };
}
