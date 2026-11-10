import { Link } from "react-router-dom";
import { ApiError } from "../../../../api/client";
import { useRead } from "../../data/provider";
import { deviceReading, deviceRequest, lightEntities, lightReading, MAX_DEVICE_ROWS, type FieldDevice, type LightReading } from "../../bindings/device";
import type { Resolved } from "../../bindings/types";
import { CardFrame, StatusList } from "../CardFrame";
import { statusEntry } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates, type ReadingRow } from "../reading-list/ReadingRows";
import { BrightnessSlider } from "../../controls/BrightnessSlider";
import { ControlStatus } from "../../controls/ControlStatus";
import { PowerSwitch } from "../../controls/PowerSwitch";
import { supportsOperation } from "../../controls/support";

/**
 * The reading shows observed State. While a Command is pending, and until the
 * State refresh that follows it lands, the switch and slider show the request.
 */
function Light({ light, rows, name, canonicalPower, canonicalBrightness }: { light: LightReading; rows: ReturnType<typeof rowStates>; name: string; canonicalPower?: LightReading["power"]; canonicalBrightness?: LightReading["brightness"] }) {
  const power = rows.find(row => row.key === light.power.id)!;
  const brightness = rows.find(row => row.key === light.brightness.id)!;
  const color = rows.find(row => row.key === light.color?.id);
  const empty = power.status.value === "empty";
  const dimmed = power.status.value === "dimmed" || brightness.status.value === "dimmed";
  const mode = rows.find(row => row.key === light.mode?.id);
  const colorDimmed = color?.status.value === "dimmed" || mode?.status.value === "dimmed";
  const level = brightness.status.value !== "empty" && light.level ? light.level.value.toFixed(light.level.format.fraction_digits) : undefined;
  const switchable = supportsOperation(light.power, "set");
  const dimmable = supportsOperation(light.brightness, "set");
  // The slider states the level, so the detail line doesn't repeat it.
  const detailLevel = !light.on && level !== undefined && !dimmable;
  return <>
    <div className="view-light">
      <span className="view-light-swatch" aria-hidden="true" data-off={!light.on || empty || undefined} data-dimmed={dimmed || colorDimmed || undefined} style={{ backgroundColor: light.on && !empty ? light.swatch : undefined }} />
      <p className="view-metric-value" data-value={empty ? "empty" : dimmed ? "dimmed" : "live"}>
        {empty ? "—" : light.on ? <>On{level !== undefined && <small>{level} %</small>}</> : "Off"}
      </p>
      {switchable && canonicalPower && <PowerSwitch entity={canonicalPower} label={`${name} power`} />}
    </div>
    {switchable && <ControlStatus entityId={light.power.id} pending={requested => requested === true ? "Turning on…" : "Turning off…"} />}
    {!empty && (detailLevel || light.colorText) && <p className="view-light-detail">
      {detailLevel && <span data-dimmed={brightness.status.value === "dimmed" || undefined}>Brightness {level} %{light.colorText ? " · " : ""}</span>}
      <span data-dimmed={colorDimmed || undefined}>{light.colorText}</span>
    </p>}
    {dimmable && canonicalBrightness && <BrightnessSlider key={light.brightness.id} entity={canonicalBrightness} label={`${name} brightness`} />}
    {rows.filter(row => row.status.entries.length).map(row => <div className="view-light-status" key={row.key}>
      <span>{row.name}</span><StatusList entries={row.status.entries} />
    </div>)}
  </>;
}

export function DeviceCard({ deviceId, label, resource, canonical, canonicalError }: { deviceId: string; label?: string; resource: Resolved<FieldDevice>; canonical?: FieldDevice; canonicalError?: string }) {
  const device = resource.status === "missing" ? undefined : resource.data;
  const allRows: ReadingRow[] = device?.entities.map(entity => {
    const reading = deviceReading(entity);
    return { key: entity.id, name: entity.name, resource: reading };
  }) ?? [];
  const light = device ? lightReading(device.entities, canonical?.entities) : undefined;
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
  const title = label ?? device?.name ?? "Device";
  const status = device ? [...(resource.refreshFailed ? [statusEntry("refresh-failed")] : []), ...(canonicalError ? [statusEntry("error", `Couldn't load canonical light State: ${canonicalError}`)] : [])] : [statusEntry(resource.status === "missing" ? "device-missing" : resource.status === "loading" ? "loading" : "error", resource.error)];
  return <CardFrame title={title} className="view-device" status={status}
    observedAt={oldestObservation(displayed)} observedLabel="Oldest reading" arrivals={arrivals} sources={rowSources(allRows)}
    sourceExtra={<p><Link to={`/devices/${deviceId}`}>Device details</Link> <code>{deviceId}</code></p>}>
    {light && <Light light={light} rows={compositeStates} name={title} canonicalPower={canonical?.entities.find(entity => entity.id === light.power.id)} canonicalBrightness={canonical?.entities.find(entity => entity.id === light.brightness.id)} />}
    {shown.length > 0 && <ReadingRows rows={states} />}
    {device && !light && shown.length === 0 && <p className="view-empty">No readable Entities on this Device.</p>}
    {additional.length > MAX_DEVICE_ROWS && <p className="view-chart-note">Showing {MAX_DEVICE_ROWS} of {additional.length} readings. Open Source for every Entity.</p>}
    {!device && <p className="view-metric-value" data-value="empty">—</p>}
  </CardFrame>;
}

export function BoundDevice({ device, label }: { device: { kind: "device"; device_id: string }; label?: string }) {
  const snapshot = useRead(deviceRequest(device.device_id));
  const data = snapshot.data as FieldDevice | undefined;
  const needsCanonical = !!data?.entities.some(entity => entity.type === "hearth.brightness/v1") && !!data.entities.some(entity => entity.type === "hearth.power/v1");
  const canonical = useRead(needsCanonical ? deviceRequest(device.device_id, "canonical") : null);
  const missing = snapshot.error instanceof ApiError && snapshot.error.status === 404;
  return <DeviceCard deviceId={device.device_id} label={label} canonical={canonical.data as FieldDevice | undefined} canonicalError={canonical.error?.message} resource={{
    status: missing ? "missing" : data ? "ready" : snapshot.error ? "error" : "loading",
    data, error: snapshot.error?.message, refreshFailed: !!snapshot.error,
  }} />;
}
