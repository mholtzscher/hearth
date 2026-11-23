import type { ReactNode } from "react";

import { NativeSelect } from "../../../../components/native-select";
import { SegmentedControl } from "../../../../components/segmented-control";
import { cn } from "../../../../lib/utils";
import type { Quantity } from "../../bindings/quantity";
import { useRefusal } from "../../controls/command-motion";
import { SharedControlStatus } from "../../controls/shared-control-status";
import { DataCard } from "../data-card";
import { useArrival } from "../observation";
import { ReadingRow, ReadingUnit } from "../reading";
import type { ValueTreatment } from "../status";
import { useThermostat } from "./data";
import type { ThermostatProps } from "./schema";
import type { ThermostatMode, ThermostatValues } from "./state";
import { TargetBlock } from "./target-block";
import { modeWords } from "./words";

/** Up to four modes fit a touch segmented control at the minimum card width. */
const SEGMENTED_MODES = 4;

const ControlRow = ({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) => (
  <div className="border-border grid grid-cols-[minmax(0,1fr)_minmax(0,9.5rem)] items-center gap-3 border-t py-3">
    <span className="text-gray-9">{label}</span>
    {children}
  </div>
);

const readingText = (
  value: number | undefined,
  quantity: Quantity | undefined
) =>
  value === undefined || !quantity
    ? undefined
    : value.toFixed(quantity.fraction_digits);

/** Only the readings light the card's edge; targets and modes come from Commands. */
const readingArrivals = ({
  observed,
  quantities,
  observedAt,
  glows,
}: {
  observed: ThermostatValues | undefined;
  quantities: Record<string, Quantity> | undefined;
  observedAt: string | undefined;
  glows: boolean;
}) =>
  [
    {
      key: "inside",
      shown: readingText(
        observed?.current_temperature,
        quantities?.["/current_temperature"]
      ),
    },
    {
      key: "humidity",
      shown: readingText(
        observed?.current_humidity,
        quantities?.["/current_humidity"]
      ),
    },
  ].flatMap(({ key, shown }) =>
    shown === undefined || !observedAt
      ? []
      : [{ key, shown, updatedAt: observedAt, eligible: glows }]
  );

/** A compact reading row that glows when its own displayed value changes. */
const Row = ({
  name,
  value,
  quantity,
  observedAt,
  treatment,
  glows,
}: {
  name: string;
  value: number | undefined;
  quantity: Quantity | undefined;
  observedAt: string | undefined;
  treatment: ValueTreatment;
  glows: boolean;
}) => {
  const text = readingText(value, quantity);
  const arrival = useArrival(
    text === undefined || !observedAt
      ? undefined
      : { updatedAt: observedAt, shown: text, eligible: glows }
  );
  return (
    <ReadingRow
      name={name}
      value={text === undefined ? "empty" : treatment}
      arrival={arrival}
      className="border-border border-t"
    >
      {text ?? "—"}
      {text !== undefined && (
        <ReadingUnit className="ms-[3px]">{quantity?.label}</ReadingUnit>
      )}
    </ReadingRow>
  );
};

/** Mode as a touch segmented control, or a select when the modes don't fit. */
const ModeControl = ({
  modes,
  value,
  enabled,
  onChange,
  refusal,
}: {
  modes: ThermostatMode[];
  value: ThermostatMode;
  enabled: boolean;
  onChange: (mode: ThermostatMode) => void;
  refusal?: string;
}) => {
  if (modes.length === 0) {
    return null;
  }
  if (modes.length > SEGMENTED_MODES) {
    return (
      <ControlRow label="Mode">
        <NativeSelect
          className={refusal}
          size="touch"
          aria-label="Mode"
          value={value}
          disabled={!enabled}
          onChange={(event) => onChange(event.target.value as ThermostatMode)}
        >
          {modes.map((choice) => (
            <option key={choice} value={choice}>
              {modeWords[choice]}
            </option>
          ))}
        </NativeSelect>
      </ControlRow>
    );
  }
  return (
    <div className={cn("border-border grid gap-2 border-t py-3", refusal)}>
      <span className="text-gray-9">Mode</span>
      <SegmentedControl
        label="Mode"
        size="touch"
        value={value}
        disabled={!enabled}
        options={modes.map((choice) => ({
          value: choice,
          label: modeWords[choice],
        }))}
        onValueChange={onChange}
      />
    </div>
  );
};

/** The fan choices, exactly as the Entity advertises them. */
const FanControl = ({
  choices,
  value,
  enabled,
  onChange,
  refusal,
}: {
  choices: string[];
  value: string | undefined;
  enabled: boolean;
  onChange: (fan: string) => void;
  refusal?: string;
}) =>
  choices.length === 0 ? null : (
    <ControlRow label="Fan">
      <NativeSelect
        className={refusal}
        size="touch"
        aria-label="Fan"
        value={value ?? ""}
        disabled={!enabled}
        onChange={(event) => onChange(event.target.value)}
      >
        {value === undefined && (
          <option value="" disabled>
            Unknown
          </option>
        )}
        {choices.map((choice) => (
          <option key={choice} value={choice}>
            {choice}
          </option>
        ))}
      </NativeSelect>
    </ControlRow>
  );

/**
 * A thermostat led by what it is set to: the targets the current mode uses,
 * each with −/+ controls, in a block whose height never changes. The mode and
 * activity, the inside temperature and humidity, and Mode and Fan follow.
 */
export const BoundThermostat = (props: ThermostatProps) => {
  const thermostat = useThermostat(props);
  const { status, observed, shown, support, mode, fan, entity } = thermostat;
  const modeRefusal = useRefusal(mode.state);
  const fanRefusal = useRefusal(fan.state);
  const targetRefusal = useRefusal(thermostat.target.state);
  const rangeRefusal = useRefusal(thermostat.range.state);
  const { value } = status;
  const { enabled } = thermostat;
  const observedAt = thermostat.resource.data?.observed_at;
  const arrivals = readingArrivals({
    observed,
    quantities: entity?.quantities,
    observedAt,
    glows: status.glows,
  });
  return (
    <DataCard
      title={thermostat.title}
      className="@container"
      status={status.entries}
      observedAt={observedAt}
      arrivals={arrivals}
      sources={thermostat.sources}
    >
      <div className="grid gap-1 pb-3">
        <TargetBlock
          mode={shown?.mode}
          targets={thermostat.targets}
          digits={thermostat.digits}
          value={value}
          enabled={enabled}
          requested={(target) => thermostat.requested(target.key)}
          refusal={(target) =>
            target.key === "single" ? targetRefusal : rangeRefusal
          }
          step={thermostat.step}
          onStep={(result) => thermostat.sendTarget(result.request)}
        />
        <p
          data-slot="thermostat-detail"
          className="text-gray-9 data-[value=dimmed]:text-gray-8 pt-1"
          data-value={value}
        >
          {/* A blank line keeps the row reserved when there is nothing to say. */}
          {thermostat.detail ?? "\u00A0"}
        </p>
      </div>
      <ul className="my-[-4px] grid list-none">
        <Row
          name="Inside"
          value={observed?.current_temperature}
          quantity={entity?.quantities?.["/current_temperature"]}
          observedAt={observedAt}
          treatment={value}
          glows={status.glows}
        />
        {observed?.current_humidity !== undefined && (
          <Row
            name="Humidity"
            value={observed.current_humidity}
            quantity={entity?.quantities?.["/current_humidity"]}
            observedAt={observedAt}
            treatment={value}
            glows={status.glows}
          />
        )}
      </ul>
      {mode.supported && shown && (
        <ModeControl
          modes={support?.modes ?? []}
          value={shown.mode}
          refusal={modeRefusal}
          enabled={enabled}
          onChange={(next) => mode.request({ mode: next })}
        />
      )}
      {fan.supported && shown && (
        <FanControl
          choices={support?.fan_modes ?? []}
          value={shown.fan_mode}
          refusal={fanRefusal}
          enabled={enabled}
          onChange={(next) => fan.request({ fan_mode: next })}
        />
      )}
      <SharedControlStatus controls={thermostat.controls} />
    </DataCard>
  );
};
