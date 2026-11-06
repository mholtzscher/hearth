import { Link } from "react-router-dom";
import { ApiError } from "../../../../api/client";
import { useUnitSystem } from "../../../units/household";
import { useRead } from "../../data/provider";
import { deviceReading, deviceRequest, lightEntities, lightReading, MAX_DEVICE_ROWS, type FieldDevice, type LightReading } from "../../bindings/device";
import type { Resolved } from "../../bindings/types";
import type { UnitSystem } from "../../../units/units";
import { CardFrame, StatusList } from "../CardFrame";
import { statusEntry } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates, type ReadingRow } from "../reading-list/ReadingRows";

function Light({ light, rows }: { light: LightReading; rows: ReturnType<typeof rowStates> }) {
  const power = rows.find(row => row.key === light.power.id)!;
  const brightness = rows.find(row => row.key === light.brightness.id)!;
  const color = rows.find(row => row.key === light.color?.id);
  const empty = power.status.value === "empty";
  const dimmed = power.status.value === "dimmed" || brightness.status.value === "dimmed";
  const mode = rows.find(row => row.key === light.mode?.id);
  const colorDimmed = color?.status.value === "dimmed" || mode?.status.value === "dimmed";
  const level = brightness.status.value !== "empty" && light.level ? light.level.value.toFixed(light.level.format.fraction_digits) : undefined;
  return <>
    <div className="view-light">
      <span className="view-light-swatch" aria-hidden="true" data-off={!light.on || empty || undefined} data-dimmed={dimmed || colorDimmed || undefined} style={{ backgroundColor: light.on && !empty ? light.swatch : undefined }} />
      <p className="view-metric-value" data-value={empty ? "empty" : dimmed ? "dimmed" : "live"}>
        {empty ? "—" : light.on ? <>On{level !== undefined && <small>{level} %</small>}</> : "Off"}
      </p>
    </div>
    {!empty && <p className="view-light-detail">
      {!light.on && level !== undefined && <span data-dimmed={brightness.status.value === "dimmed" || undefined}>Brightness {level} %{light.colorText ? " · " : ""}</span>}
      <span data-dimmed={colorDimmed || undefined}>{light.colorText}</span>
    </p>}
    {rows.filter(row => row.status.entries.length).map(row => <div className="view-light-status" key={row.key}>
      <span>{row.name}</span><StatusList entries={row.status.entries} />
    </div>)}
  </>;
}

export function DeviceCard({ deviceId, label, resource, system }: { deviceId: string; label?: string; resource: Resolved<FieldDevice>; system: UnitSystem }) {
  const device = resource.status === "missing" ? undefined : resource.data;
  const allRows: ReadingRow[] = device?.entities.map(entity => {
    const reading = deviceReading(entity, system);
    return { key: entity.id, name: entity.name, resource: reading };
  }) ?? [];
  const light = device ? lightReading(device.entities, system) : undefined;
  const used = light && device ? lightEntities(light, device.entities) : new Set<string>();
  const additional = allRows.filter(row => !used.has(row.key));
  const shown = additional.slice(0, MAX_DEVICE_ROWS);
  const composite = allRows.filter(row => row.key === light?.power.id || row.key === light?.brightness.id || row.key === light?.color?.id || row.key === light?.mode?.id);
  const states = rowStates(shown), compositeStates = rowStates(composite);
  const powerShown = compositeStates.find(row => row.key === light?.power.id)?.data;
  const displayed = [...(powerShown ? compositeStates.filter(row => row.key !== light?.mode?.id) : []), ...states];
  const arrivals = listArrivals(displayed).filter(item => item.key !== light?.color?.id);
  if (powerShown && light?.color && light.colorText && device) {
    const colorStatus = compositeStates.find(row => row.key === light.color?.id)?.status;
    const modeStatus = compositeStates.find(row => row.key === light.mode?.id)?.status;
    // Mode and active flags can change the displayed color without a new value
    // from the selected Entity. Compare the visible result on each input's clock,
    // so an inactive color changing without a visible change stays dark.
    for (const input of device.entities.filter(entity => used.has(entity.id) && entity !== light.power && entity !== light.brightness)) {
      if (!input.state) continue;
      arrivals.push({ key: `light-color:${input.id}`, updatedAt: input.state.observed_at, shown: `${light.colorText}:${light.swatch ?? ""}`,
        eligible: !!colorStatus?.glows && input.enabled && input.availability.status === "available" && (!light.mode || !!modeStatus?.glows) });
    }
  }
  const status = device ? resource.refreshFailed ? [statusEntry("refresh-failed")] : [] : [statusEntry(resource.status === "missing" ? "device-missing" : resource.status === "loading" ? "loading" : "error", resource.error)];
  return <CardFrame title={label ?? device?.name ?? "Device"} className="view-device" status={status}
    observedAt={oldestObservation(displayed)} observedLabel="Oldest reading" arrivals={arrivals} sources={rowSources(allRows)}
    sourceExtra={<p><Link to={`/devices/${deviceId}`}>Device details</Link> <code>{deviceId}</code></p>}>
    {light && <Light light={light} rows={compositeStates} />}
    {shown.length > 0 && <ReadingRows rows={states} />}
    {device && !light && shown.length === 0 && <p className="view-empty">No readable Entities on this Device.</p>}
    {additional.length > MAX_DEVICE_ROWS && <p className="view-chart-note">Showing {MAX_DEVICE_ROWS} of {additional.length} readings. Open Source for every Entity.</p>}
    {!device && <p className="view-metric-value" data-value="empty">—</p>}
  </CardFrame>;
}

export function BoundDevice({ device, label }: { device: { kind: "device"; device_id: string }; label?: string }) {
  const snapshot = useRead(deviceRequest(device.device_id));
  const system = useUnitSystem();
  const missing = snapshot.error instanceof ApiError && snapshot.error.status === 404;
  const data = snapshot.data as FieldDevice | undefined;
  return <DeviceCard deviceId={device.device_id} label={label} system={system ?? "metric"} resource={{
    status: system === undefined ? "loading" : missing ? "missing" : data ? "ready" : snapshot.error ? "error" : "loading",
    data: system === undefined ? undefined : data, error: snapshot.error?.message, refreshFailed: !!snapshot.error,
  }} />;
}
