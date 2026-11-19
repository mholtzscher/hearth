import { useState } from "react";
import { Link } from "react-router-dom";

import { ApiError } from "../../../../api/client";
import {
  deviceReading,
  deviceRequest,
  lightEntities,
  lightReading,
  MAX_DEVICE_ROWS,
} from "../../bindings/device";
import type { FieldDevice, LightReading } from "../../bindings/device";
import type { FieldEntity } from "../../bindings/entity";
import type { Resolved } from "../../bindings/types";
import { BrightnessSlider } from "../../controls/brightness-slider";
import { ControlStatus } from "../../controls/control-status";
import { lightColorControls } from "../../controls/light-color";
import { LightColorPicker } from "../../controls/light-color-picker";
import { LightControlFeedback } from "../../controls/light-control-feedback";
import { PowerSwitch } from "../../controls/power-switch";
import { supportsOperation } from "../../controls/support";
import { useRead } from "../../data/provider";
import { DataCard, StatusList } from "../data-card";
import { Reading, ReadingUnit } from "../reading";
import {
  listArrivals,
  oldestObservation,
  ReadingRows,
  rowSources,
  rowStates,
} from "../reading-list/reading-rows";
import type { ReadingRow } from "../reading-list/reading-rows";
import { statusEntry } from "../status";

type CardRows = ReturnType<typeof rowStates>;
type CardArrivals = ReturnType<typeof listArrivals>;

const requiredCompositeRow = (rows: CardRows, key: string) => {
  const row = rows.find((candidate) => candidate.key === key);
  if (!row) {
    throw new Error(`Missing composite row ${key}`);
  }
  return row;
};

const LightSwatch = ({
  light,
  empty,
  dimmed,
  colorDimmed,
}: {
  light: LightReading;
  empty: boolean;
  dimmed: boolean;
  colorDimmed: boolean;
}) => (
  <span
    data-slot="light-swatch"
    className="border-gray-5 bg-gray-9 size-8 shrink-0 rounded-full border data-[dimmed]:opacity-45 data-[off]:bg-transparent"
    aria-hidden="true"
    data-off={!light.on || empty || undefined}
    data-dimmed={dimmed || colorDimmed || undefined}
    style={{
      backgroundColor: light.on && !empty ? light.swatch : undefined,
    }}
  />
);

const LightReadingValue = ({
  empty,
  dimmed,
  on,
  dimmable,
  level,
}: {
  empty: boolean;
  dimmed: boolean;
  on: boolean | undefined;
  dimmable: boolean;
  level: string | undefined;
}) => {
  if (empty) {
    return <Reading value="empty">—</Reading>;
  }
  const value = dimmed ? "dimmed" : "live";
  if (on) {
    return (
      <Reading value={value}>
        On
        {!dimmable && level !== undefined && (
          <ReadingUnit>{level} %</ReadingUnit>
        )}
      </Reading>
    );
  }
  return <Reading value={value}>Off</Reading>;
};

const LightDetailLine = ({
  detailLevel,
  level,
  colorText,
  colorDimmed,
  brightnessDimmed,
}: {
  detailLevel: boolean;
  level: string | undefined;
  colorText: string | undefined;
  colorDimmed: boolean;
  brightnessDimmed: boolean;
}) => (
  <p
    data-slot="light-detail"
    className="text-label text-gray-9 mt-2 tabular-nums"
  >
    {detailLevel && (
      <span
        className="data-[dimmed]:text-gray-8"
        data-dimmed={brightnessDimmed || undefined}
      >
        Brightness {level} %{colorText ? " · " : ""}
      </span>
    )}
    <span
      className="data-[dimmed]:text-gray-8"
      data-dimmed={colorDimmed || undefined}
    >
      {colorText}
    </span>
  </p>
);

const LightStatusRows = ({ rows }: { rows: CardRows }) => (
  <>
    {rows
      .filter((row) => row.status.entries.length)
      .map((row) => (
        <div
          data-slot="light-status"
          className="text-muted-foreground mt-2 flex flex-wrap gap-x-2 gap-y-1"
          key={row.key}
        >
          <span>{row.name}</span>
          <StatusList entries={row.status.entries} />
        </div>
      ))}
  </>
);

const powerPendingLabel = (requested: unknown) =>
  requested === true ? "Turning on…" : "Turning off…";

const LightFeedback = ({
  light,
  controls,
  dimmable,
  switchable,
  colorOpen,
  onDetails,
}: {
  light: LightReading;
  controls: ReturnType<typeof lightColorControls>;
  dimmable: boolean;
  switchable: boolean;
  colorOpen: boolean;
  onDetails: () => void;
}) => {
  if (!switchable) {
    return null;
  }
  const adjustableColor = !!controls.temperature || !!controls.color;
  if (dimmable) {
    return adjustableColor ? (
      <LightControlFeedback
        controls={{}}
        powerId={light.power.id}
        hidden={colorOpen}
        onDetails={onDetails}
      />
    ) : (
      <ControlStatus entityId={light.power.id} pending={powerPendingLabel} />
    );
  }
  if (adjustableColor) {
    return (
      <LightControlFeedback
        controls={controls}
        brightnessId={undefined}
        powerId={light.power.id}
        hidden={colorOpen}
        onDetails={onDetails}
      />
    );
  }
  return (
    <ControlStatus entityId={light.power.id} pending={powerPendingLabel} />
  );
};

const lightFlags = (rows: CardRows, light: LightReading) => {
  const power = requiredCompositeRow(rows, light.power.id);
  const brightness = requiredCompositeRow(rows, light.brightness.id);
  const color = rows.find((row) => row.key === light.color?.id);
  const mode = rows.find((row) => row.key === light.mode?.id);
  const level =
    brightness.status.value !== "empty" && light.level
      ? light.level.value.toFixed(light.level.format.fraction_digits)
      : undefined;
  return {
    brightness,
    empty: power.status.value === "empty",
    dimmed:
      power.status.value === "dimmed" || brightness.status.value === "dimmed",
    colorDimmed:
      color?.status.value === "dimmed" || mode?.status.value === "dimmed",
    level,
  };
};

const lightSliderFeedback = ({
  light,
  controls,
  dimmable,
  switchable,
  colorOpen,
  onDetails,
}: {
  light: LightReading;
  controls: ReturnType<typeof lightColorControls>;
  dimmable: boolean;
  switchable: boolean;
  colorOpen: boolean;
  onDetails: () => void;
}) => {
  const adjustableColor = !!controls.temperature || !!controls.color;
  if (!adjustableColor) {
    return;
  }
  return (
    <LightControlFeedback
      controls={controls}
      brightnessId={dimmable ? light.brightness.id : undefined}
      powerId={!dimmable && switchable ? light.power.id : undefined}
      hidden={colorOpen}
      onDetails={onDetails}
    />
  );
};

/**
 * The reading shows observed State. While a Command is pending, and until the
 * State refresh that follows it lands, the switch and slider show the request.
 */
const Light = ({
  light,
  rows,
  name,
  entities,
}: {
  light: LightReading;
  rows: CardRows;
  name: string;
  entities: FieldEntity[];
}) => {
  const [colorOpen, setColorOpen] = useState(false);
  const { brightness, empty, dimmed, colorDimmed, level } = lightFlags(
    rows,
    light
  );
  const switchable = supportsOperation(light.power, "set");
  const dimmable = supportsOperation(light.brightness, "set");
  const controls = lightColorControls(entities);
  const feedback = lightSliderFeedback({
    light,
    controls,
    dimmable,
    switchable,
    colorOpen,
    onDetails: () => setColorOpen(true),
  });
  // The slider states the level, so the detail line doesn't repeat it.
  const detailLevel = !light.on && level !== undefined && !dimmable;
  return (
    <>
      <div data-slot="light" className="flex items-center gap-3.5">
        <LightSwatch
          light={light}
          empty={empty}
          dimmed={dimmed}
          colorDimmed={colorDimmed}
        />
        <LightReadingValue
          empty={empty}
          dimmed={dimmed}
          on={light.on}
          dimmable={dimmable}
          level={level}
        />
        <LightColorPicker
          controls={controls}
          activeId={light.color?.id}
          name={name}
          observed={light.colorText}
          open={colorOpen}
          onOpenChange={setColorOpen}
          brightnessId={dimmable ? light.brightness.id : undefined}
          powerId={switchable ? light.power.id : undefined}
          inlineFeedback={!dimmable && !switchable}
        />
        {switchable && (
          <PowerSwitch entity={light.power} label={`${name} power`} />
        )}
      </div>
      <LightFeedback
        light={light}
        controls={controls}
        dimmable={dimmable}
        switchable={switchable}
        colorOpen={colorOpen}
        onDetails={() => setColorOpen(true)}
      />
      {!empty && (detailLevel || light.colorText) && (
        <LightDetailLine
          detailLevel={detailLevel}
          level={level}
          colorText={light.colorText}
          colorDimmed={colorDimmed}
          brightnessDimmed={brightness.status.value === "dimmed"}
        />
      )}
      {light.colorPreviewUnavailable && (
        <p className="text-label text-muted-foreground mb-2">
          Color preview unavailable; canonical reading is missing or out of
          date.
        </p>
      )}
      {dimmable && (
        <BrightnessSlider
          key={light.brightness.id}
          entity={light.brightness}
          label={`${name} brightness`}
          feedback={feedback}
        />
      )}
      <LightStatusRows rows={rows} />
    </>
  );
};

const deviceRows = (device: FieldDevice | undefined): ReadingRow[] =>
  device?.entities.map((entity) => {
    const reading = deviceReading(entity);
    return { key: entity.id, name: entity.name, resource: reading };
  }) ?? [];

const lightColorArrivals = (
  device: FieldDevice,
  light: LightReading,
  used: Set<string>,
  compositeStates: CardRows,
  arrivals: CardArrivals
): CardArrivals => {
  if (!(light.color && light.colorText)) {
    return arrivals;
  }
  const colorStatus = compositeStates.find(
    (row) => row.key === light.color?.id
  )?.status;
  const modeStatus = compositeStates.find(
    (row) => row.key === light.mode?.id
  )?.status;
  const next = [...arrivals];
  // Mode and active flags can change the displayed color without a new value
  // from the selected Entity. Compare the visible result on each input's clock,
  // so an inactive color changing without a visible change stays dark.
  for (const input of device.entities.filter(
    (entity) =>
      used.has(entity.id) &&
      entity !== light.power &&
      entity !== light.brightness
  )) {
    if (!input.state) {
      continue;
    }
    next.push({
      key: `light-color:${input.id}`,
      updatedAt: input.state.observed_at,
      shown: `${light.colorText}:${light.swatch ?? ""}`,
      eligible:
        !!colorStatus?.glows &&
        input.enabled &&
        input.availability.status === "available" &&
        (!light.mode || !!modeStatus?.glows),
    });
  }
  return next;
};

const cardDisplayRows = (
  allRows: ReadingRow[],
  states: CardRows,
  light: LightReading | undefined,
  device: FieldDevice | undefined,
  used: Set<string>
) => {
  const composite = allRows.filter(
    (row) =>
      row.key === light?.power.id ||
      row.key === light?.brightness.id ||
      row.key === light?.color?.id ||
      row.key === light?.mode?.id
  );
  const compositeStates = rowStates(composite);
  const powerShown = compositeStates.find(
    (row) => row.key === light?.power.id
  )?.data;
  const displayed = powerShown
    ? [
        ...compositeStates.filter((row) => row.key !== light?.mode?.id),
        ...states,
      ]
    : states;
  const baseArrivals = listArrivals(displayed).filter(
    (item) => item.key !== light?.color?.id
  );
  const arrivals =
    device && light && powerShown
      ? lightColorArrivals(device, light, used, compositeStates, baseArrivals)
      : baseArrivals;
  return { compositeStates, displayed, arrivals };
};

const readingStatusKey = (status: Resolved<FieldDevice>["status"]) => {
  if (status === "missing") {
    return "device-missing";
  }
  if (status === "loading") {
    return "loading";
  }
  return "error";
};

const deviceCardStatus = (
  device: FieldDevice | undefined,
  resource: Resolved<FieldDevice>,
  canonicalError: string | undefined
) => {
  if (!device) {
    return [statusEntry(readingStatusKey(resource.status), resource.error)];
  }
  return [
    ...(resource.refreshFailed ? [statusEntry("refresh-failed")] : []),
    ...(canonicalError
      ? [
          statusEntry(
            "error",
            `Couldn't load canonical light State: ${canonicalError}`
          ),
        ]
      : []),
  ];
};

const DeviceReadingNotice = ({
  additionalCount,
}: {
  additionalCount: number;
}) => {
  if (additionalCount <= MAX_DEVICE_ROWS) {
    return null;
  }
  return (
    <p className="text-label text-muted-foreground mb-2">
      Showing {MAX_DEVICE_ROWS} of {additionalCount} readings. Open Source for
      every Entity.
    </p>
  );
};

export const DeviceCard = ({
  deviceId,
  label,
  resource,
  canonical,
  canonicalError,
}: {
  deviceId: string;
  label?: string;
  resource: Resolved<FieldDevice>;
  canonical?: FieldDevice;
  canonicalError?: string;
}) => {
  const device = resource.status === "missing" ? undefined : resource.data;
  const allRows = deviceRows(device);
  const light = device
    ? lightReading(
        device.entities,
        canonicalError ? undefined : canonical?.entities
      )
    : undefined;
  const used =
    light && device ? lightEntities(light, device.entities) : new Set<string>();
  const additional = allRows.filter((row) => !used.has(row.key));
  const shown = additional.slice(0, MAX_DEVICE_ROWS);
  const states = rowStates(shown);
  const { compositeStates, displayed, arrivals } = cardDisplayRows(
    allRows,
    states,
    light,
    device,
    used
  );
  const title = label ?? device?.name ?? "Device";
  const status = deviceCardStatus(device, resource, canonicalError);
  return (
    <DataCard
      title={title}
      status={status}
      observedAt={oldestObservation(displayed)}
      observedLabel="Oldest reading"
      arrivals={arrivals}
      sources={rowSources(allRows)}
      sourceExtra={
        <p>
          <Link
            className="text-link focus-visible:outline-ring underline underline-offset-[3px] focus-visible:outline-2 focus-visible:outline-offset-3"
            to={`/devices/${deviceId}`}
          >
            Device details
          </Link>{" "}
          <code className="text-label text-muted-foreground mt-1 block">
            {deviceId}
          </code>
        </p>
      }
    >
      {light && device && (
        <Light
          light={light}
          rows={compositeStates}
          name={title}
          entities={device.entities}
        />
      )}
      {shown.length > 0 && (
        <ReadingRows
          className={light && device ? "mt-3" : undefined}
          rows={states}
        />
      )}
      {device && !light && shown.length === 0 && (
        <p className="text-label text-muted-foreground py-6">
          No readable Entities on this Device.
        </p>
      )}
      <DeviceReadingNotice additionalCount={additional.length} />
      {!device && <Reading value="empty">—</Reading>}
    </DataCard>
  );
};

const deviceResourceStatus = (
  missing: boolean,
  data: FieldDevice | undefined,
  error: Error | undefined
): Resolved<FieldDevice>["status"] => {
  if (missing) {
    return "missing";
  }
  if (data) {
    return "ready";
  }
  if (error) {
    return "error";
  }
  return "loading";
};

export const BoundDevice = ({
  device,
  label,
}: {
  device: { kind: "device"; device_id: string };
  label?: string;
}) => {
  const snapshot = useRead(deviceRequest(device.device_id));
  const data = snapshot.data as FieldDevice | undefined;
  const color = data && lightReading(data.entities)?.color;
  const needsCanonical = color?.type === "hearth.colorxy/v1" && !!color.state;
  const canonical = useRead(
    needsCanonical ? deviceRequest(device.device_id, "canonical") : null
  );
  const missing =
    snapshot.error instanceof ApiError && snapshot.error.status === 404;
  return (
    <DeviceCard
      deviceId={device.device_id}
      label={label}
      canonical={canonical.data as FieldDevice | undefined}
      canonicalError={canonical.error?.message}
      resource={{
        status: deviceResourceStatus(missing, data, snapshot.error),
        data,
        error: snapshot.error?.message,
        refreshFailed: !!snapshot.error,
      }}
    />
  );
};
