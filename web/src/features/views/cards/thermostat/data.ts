import { ApiError } from "../../../../api/client";
import {
  entityMetadata,
  entityStateRequest,
  readEntity,
} from "../../bindings/entity";
import type { FieldEntity } from "../../bindings/entity";
import { readErrorMessage } from "../../bindings/history";
import type { Quantity } from "../../bindings/quantity";
import type { Resolved, ResourceMetadata } from "../../bindings/types";
import type { ControlState, ControlTarget } from "../../controls/queue";
import type { StatusControl } from "../../controls/shared-control-status";
import { supportsOperation } from "../../controls/support";
import { useControl, useFastRefresh, useRead } from "../../data/provider";
import type { SourceItem } from "../data-card";
import { cardStatus } from "../status";
import type { CardStatus } from "../status";
import type { ThermostatProps } from "./schema";
import { thermostatSupport, thermostatValues } from "./state";
import type {
  ThermostatMode,
  ThermostatSupport,
  ThermostatValues,
} from "./state";
import { stepTarget, targetFields, targetsFor } from "./targets";
import type { Target, TargetKey, TargetRequest } from "./targets";
import {
  formatTarget,
  fromMilliCelsius,
  roundTo,
  temperatureUnit,
} from "./units";
import type { TemperatureUnit } from "./units";
import { detailLine, modeWords, rangeWords } from "./words";

interface ThermostatData {
  shown: ThermostatValues;
  observed_at: string;
}

const working = (state: ControlState) =>
  state.phase === "pending" || state.phase === "confirming";

const requestedOf = <T>(state: ControlState): T | undefined =>
  working(state) && typeof state.requested === "object" && state.requested
    ? (state.requested as T)
    : undefined;

// The read validates the one required field; the whole State comes back with it.
const modeSource = (props: ThermostatProps) => ({
  ...props.source,
  field: "/mode",
});

const resolve = (
  props: ThermostatProps,
  snapshot: ReturnType<typeof useRead>
): {
  entity?: FieldEntity;
  canonical?: FieldEntity;
  resource: Resolved<ThermostatData>;
} => {
  const missing =
    snapshot.error instanceof ApiError && snapshot.error.status === 404;
  let status: Resolved<ThermostatData>["status"] = "loading";
  if (missing) {
    status = "missing";
  } else if (snapshot.error) {
    status = "error";
  }
  if (snapshot.data === undefined || missing) {
    return { resource: { status, error: readErrorMessage(snapshot.error) } };
  }
  try {
    const entity = readEntity(snapshot.data, modeSource(props));
    const canonical = readEntity(snapshot.data, modeSource(props), "canonical");
    const shown = thermostatValues(entity);
    return {
      entity,
      canonical,
      resource: {
        status: entity.state && shown ? "ready" : "unobserved",
        data:
          entity.state && shown
            ? { shown, observed_at: entity.state.observed_at }
            : undefined,
        metadata: entityMetadata(entity, props.source),
        error: readErrorMessage(snapshot.error),
        refreshFailed: !!snapshot.error,
      },
    };
  } catch {
    return {
      resource: {
        status: "error",
        error: "The resource returned incompatible data.",
      },
    };
  }
};

/** The temperature quantity whose unit and digits the targets use. */
const targetQuantity = (
  entity: FieldEntity | undefined
): Quantity | undefined =>
  entity?.quantities?.["/target_temperature_low"] ??
  entity?.quantities?.["/target_temperature_high"] ??
  entity?.quantities?.["/target_temperature"] ??
  entity?.quantities?.["/current_temperature"];

interface Requests {
  mode?: { mode: ThermostatMode };
  range?: { low: number; high: number };
  target?: { temperature: number };
  fan?: { fan_mode: string };
}

/** What controls show: observed State with each in-flight request applied. */
const applyRequests = (
  observed: ThermostatValues,
  observedCanonical: ThermostatValues,
  requests: Requests,
  toShown: (mCel: number) => number | undefined
) => {
  const shown = { ...observed };
  const canonical = { ...observedCanonical };
  if (requests.mode) {
    shown.mode = requests.mode.mode;
    canonical.mode = requests.mode.mode;
  }
  if (requests.fan) {
    shown.fan_mode = requests.fan.fan_mode;
  }
  if (requests.range) {
    canonical.target_temperature_low = requests.range.low;
    canonical.target_temperature_high = requests.range.high;
    shown.target_temperature_low = toShown(requests.range.low);
    shown.target_temperature_high = toShown(requests.range.high);
  }
  if (requests.target) {
    canonical.target_temperature = requests.target.temperature;
    shown.target_temperature = toShown(requests.target.temperature);
  }
  return { shown, canonical };
};

/** The shared status line's controls, with words for each pending request. */
const statusControls = ({
  entityId,
  supported,
  observed,
  words,
  unitLabel,
}: {
  entityId: string;
  supported: { mode: boolean; range: boolean; target: boolean; fan: boolean };
  observed: ThermostatValues | undefined;
  words: (mCel: number) => string;
  unitLabel: string;
}): StatusControl[] => {
  const controls: StatusControl[] = [];
  if (supported.mode) {
    controls.push({
      target: { entityId, operation: "set_mode" },
      name: "Mode",
      pending: (value) =>
        `Switching to ${modeWords[(value as { mode: ThermostatMode }).mode]}…`,
    });
  }
  if (supported.range) {
    controls.push({
      target: { entityId, operation: "set_range" },
      name: "Targets",
      pending: (value) => {
        const { low, high } = value as { low: number; high: number };
        return rangeWords({
          low: words(low),
          high: words(high),
          lowChanged: low !== observed?.target_temperature_low,
          highChanged: high !== observed?.target_temperature_high,
          unitLabel,
        });
      },
    });
  }
  if (supported.target) {
    const verb =
      observed?.mode === "heat" || observed?.mode === "cool"
        ? observed.mode
        : "the target";
    controls.push({
      target: { entityId, operation: "set_target" },
      name: "Target",
      pending: (value) =>
        `Setting ${verb} to ${words((value as { temperature: number }).temperature)} ${unitLabel}…`,
    });
  }
  if (supported.fan) {
    controls.push({
      target: { entityId, operation: "set_fan_mode" },
      name: "Fan",
      pending: (value) =>
        `Setting the fan to ${(value as { fan_mode: string }).fan_mode}…`,
    });
  }
  return controls;
};

/** Targets rounded to their displayed digits, so a press steps from what is shown. */
const roundTargets = (
  values: ThermostatValues | undefined,
  digits: number
): ThermostatValues | undefined => {
  if (!values) {
    return undefined;
  }
  const rounded = { ...values };
  for (const field of Object.values(targetFields)) {
    const value = rounded[field];
    if (value !== undefined) {
      rounded[field] = roundTo(value, digits);
    }
  }
  return rounded;
};

/** The unit, digits, and label targets are shown and stepped in. */
const targetFormat = (
  entity: FieldEntity | undefined
): { unit?: TemperatureUnit; digits: number; unitLabel: string } => {
  const quantity = targetQuantity(entity);
  return {
    unit: temperatureUnit(quantity),
    digits: quantity?.fraction_digits ?? 1,
    unitLabel: quantity?.label ?? "",
  };
};

const sourcesOf = (
  entityId: string,
  title: string,
  metadata: ResourceMetadata | undefined
): SourceItem[] => [
  {
    entityId,
    label: metadata?.label ?? title,
    availability: metadata?.availability,
    enabled: metadata?.enabled,
  },
];

export interface ThermostatControl<P> {
  supported: boolean;
  state: ControlState;
  request: (parameters: P) => void;
}

const useOperation = <P extends Record<string, unknown>>(
  entityId: string,
  operation: ControlTarget["operation"],
  entity: FieldEntity | undefined
): ThermostatControl<P> => {
  const [state, request] = useControl({ entityId, operation });
  return {
    supported: !!entity && supportsOperation(entity, operation),
    state,
    request,
  };
};

/**
 * Reads one thermostat and drives its controls. Controls show what was
 * requested while a Command is in flight; readings keep observed State.
 */
export const useThermostat = (props: ThermostatProps) => {
  const request = entityStateRequest(modeSource(props));
  const { entity, canonical, resource } = resolve(props, useRead(request));
  const status: CardStatus = cardStatus(resource);
  const entityId = props.source.entity_id;
  const mode = useOperation<{ mode: ThermostatMode }>(
    entityId,
    "set_mode",
    entity
  );
  const target = useOperation<{ temperature: number }>(
    entityId,
    "set_target",
    entity
  );
  const range = useOperation<{ low: number; high: number }>(
    entityId,
    "set_range",
    entity
  );
  const fan = useOperation<{ fan_mode: string }>(
    entityId,
    "set_fan_mode",
    entity
  );
  useFastRefresh(
    request,
    [mode, target, range, fan].some((control) => working(control.state))
  );

  const support: ThermostatSupport | undefined = thermostatSupport(entity);
  const { unit, digits, unitLabel } = targetFormat(entity);
  // Core doesn't round household values, so a confirmed 70 °F arrives as 69.9998.
  const observed = roundTargets(resource.data?.shown, digits);
  const observedCanonical = thermostatValues(canonical);

  const requests: Requests = {
    mode: requestedOf(mode.state),
    range: requestedOf(range.state),
    target: requestedOf(target.state),
    fan: requestedOf(fan.state),
  };
  const toShown = (mCel: number) =>
    unit ? fromMilliCelsius(mCel, unit, digits) : undefined;
  const applied =
    observed && observedCanonical
      ? applyRequests(observed, observedCanonical, requests, toShown)
      : undefined;
  const shown = applied?.shown;
  const shownCanonical = applied?.canonical;

  const targets: Target[] =
    shown && shownCanonical
      ? targetsFor({
          mode: shown.mode,
          operations: { range: range.supported, target: target.supported },
          shown,
          canonical: shownCanonical,
        })
      : [];
  /** A target is requested when an in-flight Command changes its value. */
  const requested = (key: TargetKey) => {
    const field = targetFields[key];
    const pending = key === "single" ? requests.target : requests.range;
    return (
      pending !== undefined &&
      shownCanonical?.[field] !== observedCanonical?.[field]
    );
  };
  /** One −/+ press on a target, or undefined when the press isn't allowed. */
  const step = (shownTarget: Target, direction: 1 | -1) =>
    shownCanonical
      ? stepTarget({
          target: shownTarget,
          direction,
          unit,
          support,
          canonical: shownCanonical,
        })
      : undefined;
  const sendTarget = (next: TargetRequest) => {
    if (next.operation === "set_range") {
      range.request(next.parameters);
    } else {
      target.request(next.parameters);
    }
  };

  const words = (mCel: number) =>
    unit ? formatTarget(fromMilliCelsius(mCel, unit, digits), digits) : "—";
  const controls = statusControls({
    entityId,
    supported: {
      mode: mode.supported,
      range: range.supported,
      target: target.supported,
      fan: fan.supported,
    },
    observed: observedCanonical,
    words,
    unitLabel,
  });

  const title = props.label ?? resource.metadata?.label ?? "Thermostat";
  const sources = sourcesOf(entityId, title, resource.metadata);
  return {
    entity,
    resource,
    status,
    title,
    sources,
    enabled: entity?.enabled ?? false,
    support,
    unit,
    digits,
    observed: status.value === "empty" ? undefined : observed,
    shown: status.value === "empty" ? undefined : shown,
    shownCanonical,
    targets,
    requested,
    detail: observed && detailLine(observed, targets.length > 0),
    step,
    sendTarget,
    target,
    range,
    mode,
    fan,
    controls,
  };
};
