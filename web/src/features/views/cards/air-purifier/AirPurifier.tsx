import { useControls } from "../../data/provider";
import { PowerSwitch } from "../../controls/PowerSwitch";
import { SharedControlStatus, type StatusControl } from "../../controls/SharedControlStatus";
import { supportsOperation } from "../../controls/support";
import { CardFrame, StatusList } from "../CardFrame";
import { Reading, ReadingRow, ReadingUnit } from "../Reading";
import { useArrival } from "../observation";
import { listArrivals, oldestObservation, shownReading } from "../reading-list/ReadingRows";
import { FanStepper } from "./FanStepper";
import { ModeControl } from "./ModeControl";
import { AirPurifierTile } from "./AirPurifierTile";
import { choiceWords, choicesOf } from "./category";
import { Category, usePurifier } from "./data";
import type { AirPurifierProps, AirPurifierRole } from "./schema";

type Row = ReturnType<typeof usePurifier>["rows"][number];
function NumericRow({ row, hideUnavailable }: { row: Row; hideUnavailable: boolean }) {
  const data = row.data && "value" in row.data ? row.data : undefined;
  const glow = useArrival(data ? { updatedAt: data.observed_at, shown: shownReading(data), eligible: row.status.glows } : undefined);
  return <ReadingRow name={row.name} value={row.status.value} arrival={glow} className="border-t border-border"
    beforeValue={row.key === "pm25" && data && <Category value={data.value} dimmed={row.status.value === "dimmed"} />}
    afterValue={row.key === "filter_life" && data && data.value <= 10 && <span className="basis-full text-warning">Replace the filter soon</span>}
    status={hideUnavailable ? row.status.entries.filter(entry => entry.key !== "unavailable") : row.status.entries}>
    {data ? <>{data.value.toFixed(data.format.fraction_digits)}<ReadingUnit className="ms-[3px]">{data.format.unit_label}</ReadingUnit></> : "—"}
  </ReadingRow>;
}

function RoleStatus({ row, hideUnavailable }: { row: Row; hideUnavailable: boolean }) {
  const entries = hideUnavailable ? row.status.entries.filter(entry => entry.key !== "unavailable") : row.status.entries;
  return entries.length > 0 ? <div className="mb-2 flex flex-wrap gap-x-2 gap-y-0.5 text-muted-foreground"><span>{row.name}</span><StatusList entries={entries} /></div> : null;
}

export function AirPurifierCard({ props, title }: { props: AirPurifierProps; title: string }) {
  const { rows, entities, sources, status, allUnavailable, sourceExtra } = usePurifier(props);
  const row = (role: AirPurifierRole) => rows.find(item => item.key === role);
  const power = row("power"), mode = row("mode"), fan = row("fan_level");
  const controllable = (role: AirPurifierRole) => !!entities[role] && supportsOperation(entities[role]!, "set");
  const controls: StatusControl[] = [];
  if (controllable("power")) controls.push({ target: { entityId: props.power.entity_id, operation: "set" }, name: "Power", pending: value => value === true ? "Turning on…" : "Turning off…" });
  if (props.mode && controllable("mode")) controls.push({ target: { entityId: props.mode.entity_id, operation: "set" }, name: "Mode", pending: value => `Switching to ${choiceWords(String(value))}…` });
  if (props.fan_level && controllable("fan_level")) controls.push({ target: { entityId: props.fan_level.entity_id, operation: "set" }, name: "Fan level", pending: value => `Setting fan level to ${value}…` });
  if (props.display && controllable("display")) controls.push({ target: { entityId: props.display.entity_id, operation: "set" }, name: "Display", pending: value => `Turning the display ${value === true ? "on" : "off"}…` });
  if (props.child_lock && controllable("child_lock")) controls.push({ target: { entityId: props.child_lock.entity_id, operation: "set" }, name: "Child lock", pending: value => value === true ? "Locking the controls…" : "Unlocking the controls…" });
  const [modeStates] = useControls(props.mode ? [{ entityId: props.mode.entity_id, operation: "set" }] : []);
  const modeState = modeStates[0];
  const shownMode = modeState && (modeState.phase === "pending" || modeState.phase === "confirming") ? modeState.requested : entities.mode?.state?.value;
  const active = !props.mode || !choicesOf(entities.mode?.support).includes("manual") || shownMode === "manual";
  const observedMode = mode?.data && "text" in mode.data ? mode.data.text : undefined;
  const observedFan = fan?.data && "text" in fan.data ? fan.data.text : undefined;
  const numeric = rows.filter(item => item.key === "pm25" || item.key === "filter_life");
  // The footer states the oldest reading the card shows; a read-only setting it hides doesn't count.
  const shown = rows.filter(item => item.key === "power" || item.key === "pm25" || item.key === "filter_life" ||
    (item.key === "mode" && observedMode !== undefined) ||
    (item.key === "fan_level" && (controllable("fan_level") || observedMode === "manual")) ||
    ((item.key === "display" || item.key === "child_lock") && controllable(item.key)));
  return <CardFrame title={title} className="@container" status={status} observedAt={oldestObservation(shown)} observedLabel="Oldest reading" arrivals={listArrivals(numeric)} sources={sources} sourceExtra={sourceExtra}>
    <div className="flex items-center gap-3.5 pb-3"><div className="min-w-0 flex-1"><Reading value={power?.status.value}>{power?.data && "text" in power.data ? power.data.text : "—"}</Reading>
      {observedMode && <p data-slot="purifier-detail" className="text-gray-9 tabular-nums data-[value=dimmed]:text-gray-8" data-value={mode?.status.value}>{choiceWords(observedMode)}{observedMode === "manual" && observedFan && ` · fan ${observedFan}`}</p>}</div>
      {controllable("power") && <PowerSwitch entity={entities.power!} label={`${title} power`} />}
    </div>
    {power && <StatusList entries={allUnavailable ? power.status.entries.filter(entry => entry.key !== "unavailable") : power.status.entries} />}
    <ul className="my-[-4px] grid list-none">{numeric.map(item => <NumericRow key={item.key} row={item} hideUnavailable={allUnavailable} />)}</ul>
    {controllable("mode") && <ModeControl entity={entities.mode!} label="Mode" />}
    {controllable("fan_level") && <FanStepper entity={entities.fan_level!} levels={choicesOf(entities.fan_level?.support).length} active={active} label="Fan level" />}
    {(["mode", "fan_level"] as const).map(role => { const item = row(role); return item && <RoleStatus key={role} row={item} hideUnavailable={allUnavailable} />; })}
    {(controllable("display") || controllable("child_lock")) && <div className="grid grid-cols-2 gap-x-5 gap-y-1 border-t border-border pt-3 pb-0.5 @max-[260px]:grid-cols-1">{(["display", "child_lock"] as const).map(role => controllable(role) && <div className="flex min-h-11 items-center justify-between gap-2.5 text-gray-9" key={role}><span>{role === "display" ? "Display" : "Child lock"}</span><PowerSwitch entity={entities[role]!} label={role === "display" ? "Display" : "Child lock"} /></div>)}</div>}
    {(["display", "child_lock"] as const).map(role => { const item = row(role); return item && <RoleStatus key={role} row={item} hideUnavailable={allUnavailable} />; })}
    <SharedControlStatus controls={controls} />
  </CardFrame>;
}

export function BoundAirPurifier(props: AirPurifierProps) {
  const title = props.label ?? "Air purifier";
  return props.layout === "tile" ? <AirPurifierTile props={props} title={title} /> : <AirPurifierCard props={props} title={title} />;
}
