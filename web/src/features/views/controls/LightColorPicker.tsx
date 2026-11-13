import { useEffect, useId, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import { ChevronRightIcon, XIcon } from "lucide-react";
import { Dialog, Popover } from "radix-ui";
import type { FieldEntity } from "../bindings/entity";
import { pointer } from "../bindings/state-fields";
import { ControlStatus } from "./ControlStatus";
import { ColorWheel } from "./ColorWheel";
import { useColorSetting } from "./useColorSetting";
import { colorHex, colorHS, colorRGB, hsRGB, lightColorControls, parseHex, readColor, rgbHS, type ColorControl, type ColorValue, type HS, type LightColorControls, type RGB, type TemperatureControl } from "./light-color";

const phoneQuery = "(max-width: 720px)";
function subscribePhone(change: () => void) {
  const media = window.matchMedia?.(phoneQuery);
  media?.addEventListener("change", change);
  return () => media?.removeEventListener("change", change);
}
const isPhone = () => window.matchMedia?.(phoneQuery).matches ?? false;

function Range({ label, value, valueText, minimum, maximum, step = 1, disabled, pending, spectrum, onBegin, onChange, onEnd, onCancel, onBlur }: {
  label: string; value: number; valueText: string; minimum: number; maximum: number; step?: number;
  disabled: boolean; pending: boolean; spectrum?: string;
  onBegin: () => void; onChange: (value: number) => void; onEnd: (value: number) => void; onCancel: () => void; onBlur: () => void;
}) {
  const id = useId();
  return <div className="view-color-range">
    <div className="view-slider-label"><label htmlFor={id}>{label}</label><span className="view-slider-value" aria-hidden="true">{valueText}</span></div>
    <input id={id} type="range" className="view-slider view-color-spectrum" value={value} min={minimum} max={maximum} step={step}
      aria-valuetext={valueText} aria-busy={pending || undefined} disabled={disabled}
      style={{ "--spectrum": spectrum } as CSSProperties}
      onPointerDown={onBegin} onPointerUp={event => onEnd(Number(event.currentTarget.value))}
      onPointerCancel={onCancel} onChange={event => onChange(Number(event.currentTarget.value))} onBlur={onBlur} />
  </div>;
}

function Temperature({ control }: { control: TemperatureControl }) {
  const { entity, minimum, maximum, step } = control;
  const read = (raw: unknown) => {
    const value = typeof raw === "number" ? raw : pointer(raw, "/value");
    return typeof value === "number" && Number.isFinite(value) ? value : undefined;
  };
  const setting = useColorSetting({ entity, read, fallback: minimum });
  const [exact, setExact] = useState(String(setting.value));
  const id = useId();
  useEffect(() => setExact(String(setting.value)), [setting.value]);
  const snap = (value: number) => Math.max(minimum, Math.min(maximum, Math.round(value / step) * step));
  return <fieldset disabled={!entity.enabled} className="view-color-editor">
    <legend className="sr-only">White temperature</legend>
    {!entity.enabled && <p className="view-color-hint">This Entity is disabled.</p>}
    {setting.observed === undefined && <p className="view-color-hint">Temperature has not been observed yet.</p>}
    <Range label="Color temperature" value={-snap(setting.value)} valueText={`${setting.value} mired · ${Math.round(1_000_000 / setting.value).toLocaleString()} K`}
      minimum={-maximum} maximum={-minimum} step={step} disabled={!entity.enabled} pending={setting.pending}
      spectrum="linear-gradient(90deg,#ff9f43,#fff1de,#a8c8ff)"
      onBegin={setting.begin} onChange={value => setting.change(-value)} onEnd={value => setting.end(-value)} onCancel={setting.cancel} onBlur={setting.blur} />
    <div className="view-color-ends"><span>Warm · {Math.round(1_000_000 / maximum).toLocaleString()} K</span><span>Cool · {Math.round(1_000_000 / minimum).toLocaleString()} K</span></div>
    <form className="view-color-exact" onSubmit={event => {
      event.preventDefault();
      if (event.currentTarget.reportValidity()) setting.commit(Number(exact));
    }}>
      <label htmlFor={id}>Exact value <input id={id} type="number" min={minimum} max={maximum} step={step} required value={exact} onChange={event => setExact(event.target.value)} /> mired</label>
      <button type="submit">Set white</button>
    </form>
  </fieldset>;
}

function ExactColor({ value, disabled, onCommit }: { value: RGB; disabled: boolean; onCommit: (value: RGB) => void }) {
  const [channels, setChannels] = useState([String(value.red), String(value.green), String(value.blue)]);
  const [hex, setHex] = useState(colorHex(value));
  const id = useId();
  useEffect(() => { setChannels([String(value.red), String(value.green), String(value.blue)]); setHex(colorHex(value)); }, [value.red, value.green, value.blue]);
  return <div className="view-color-precision">
    <form className="view-color-exact" onSubmit={event => {
      event.preventDefault();
      if (event.currentTarget.reportValidity()) onCommit({ red: Number(channels[0]), green: Number(channels[1]), blue: Number(channels[2]) });
    }}>
      <div className="view-color-channels">{["R", "G", "B"].map((channel, index) => <label key={channel} htmlFor={`${id}-${channel}`}>{channel}
        <input id={`${id}-${channel}`} type="number" min={0} max={255} step={1} required disabled={disabled}
          value={channels[index]} onChange={event => setChannels(channels.map((value, i) => i === index ? event.target.value : value))} />
      </label>)}</div>
      <button type="submit" disabled={disabled}>Set RGB</button>
    </form>
    <form className="view-color-exact" onSubmit={event => {
      event.preventDefault();
      const parsed = parseHex(hex);
      if (event.currentTarget.reportValidity() && parsed) onCommit(parsed);
    }}>
      <label htmlFor={`${id}-hex`}>Hex <input id={`${id}-hex`} type="text" pattern="#?[0-9a-fA-F]{6}" maxLength={7} required spellCheck={false}
        value={hex} disabled={disabled} onChange={event => setHex(event.target.value)} /></label>
      <button type="submit" disabled={disabled}>Set hex</button>
    </form>
  </div>;
}

function Color({ control }: { control: ColorControl }) {
  const { entity, kind } = control;
  const setting = useColorSetting<ColorValue>({ entity, read: value => readColor(value, kind), fallback: kind === "rgb" ? { red: 255, green: 255, blue: 255 } : { hue: 0, saturation: 0 } });
  // White has no encoded hue, and low RGB levels quantize small hue changes.
  // Retain the editing intent while its encoded RGB still matches the setting.
  const [intent, setIntent] = useState<{ hs: HS; hex: string }>();
  useEffect(() => { if (setting.failed) setIntent(undefined); }, [setting.failed]);
  const hs = intent?.hex === colorHex(setting.value) ? intent.hs : colorHS(setting.value), rgb = colorRGB(setting.value);
  const fromHS = (value: HS): ColorValue => kind === "hs" ? value : hsRGB(value, Math.max(rgb.red, rgb.green, rgb.blue) || 255);
  function previewHS(value: HS) {
    const encoded = fromHS(value);
    setIntent({ hs: value, hex: colorHex(encoded) });
    setting.change(encoded);
  }
  function endHS(value: HS) {
    const encoded = fromHS(value);
    setIntent({ hs: value, hex: colorHex(encoded) });
    setting.end(encoded);
  }
  function cancelHS() { setIntent(undefined); setting.cancel(); }
  return <fieldset className="view-color-editor" disabled={!entity.enabled}>
    <legend className="sr-only">Light color</legend>
    {!entity.enabled && <p className="view-color-hint">This Entity is disabled.</p>}
    {setting.observed === undefined && <p className="view-color-hint">Color has not been observed yet.</p>}
    <ColorWheel value={hs} disabled={!entity.enabled} pending={setting.pending}
      onBegin={setting.begin} onChange={previewHS} onEnd={endHS} onCancel={cancelHS} onBlur={setting.blur} />
    <Range label="Hue" value={hs.hue} valueText={`${hs.hue}°`} minimum={0} maximum={359} disabled={!entity.enabled} pending={setting.pending}
      spectrum="linear-gradient(90deg,#f66,#ff6,#6f6,#6ff,#66f,#f6f,#f66)"
      onBegin={setting.begin} onChange={hue => previewHS({ ...hs, hue })} onEnd={hue => endHS({ ...hs, hue })} onCancel={cancelHS} onBlur={setting.blur} />
    <Range label="Saturation" value={hs.saturation} valueText={`${hs.saturation} %`} minimum={0} maximum={100} disabled={!entity.enabled} pending={setting.pending}
      spectrum={`linear-gradient(90deg,#fff,hsl(${hs.hue} 100% 50%))`}
      onBegin={setting.begin} onChange={saturation => previewHS({ ...hs, saturation })} onEnd={saturation => endHS({ ...hs, saturation })} onCancel={cancelHS} onBlur={setting.blur} />
    <ExactColor value={rgb} disabled={!entity.enabled} onCommit={rgb => { setIntent(undefined); setting.commit(kind === "rgb" ? rgb : rgbHS(rgb)); }} />
    <p className="view-color-hint">{kind === "hs" ? "This light stores hue and saturation. RGB is converted; brightness stays unchanged." : "Exact RGB sets the color. Brightness is controlled separately."}</p>
  </fieldset>;
}

function Feedback({ controls }: { controls: LightColorControls }) {
  const colorControl = controls.color;
  return <div className="view-color-feedback">
    {controls.temperature && <ControlStatus entityId={controls.temperature.entity.id} pending={value => `Setting white to ${String(value)} mired…`} />}
    {colorControl && <ControlStatus entityId={colorControl.entity.id} pending={value => {
      const color = readColor(value, colorControl.kind);
      return color ? `Setting color to ${colorHex(color)}…` : "Setting color…";
    }} />}
  </div>;
}

/** Capability-derived color control; the queue and observed reading outlive the overlay. */
export function LightColorPicker({ entities, activeId, name, observed, swatch }: {
  entities: FieldEntity[]; activeId?: string; name: string; observed?: string; swatch?: string;
}) {
  const controls = lightColorControls(entities);
  const [open, setOpen] = useState(false);
  const phone = useSyncExternalStore(subscribePhone, isPhone);
  const [mode, setMode] = useState<"white" | "color">("white");
  const headingId = useId(), descriptionId = useId();
  if (!controls.temperature && !controls.color) return null;
  const selected = mode === "white" && controls.temperature ? "white" : controls.color ? "color" : "white";
  const both = !!controls.temperature && !!controls.color;
  const triggerText = both ? "Adjust white or color" : controls.temperature ? "Adjust white" : "Adjust color";
  function changeOpen(next: boolean) {
    if (next) setMode(controls.color?.entity.id === activeId ? "color" : controls.temperature ? "white" : "color");
    setOpen(next);
  }
  const trigger = <button type="button" className="view-color-trigger" aria-label={`${name}: ${triggerText}`} aria-expanded={open}>
    <span className="view-color-trigger-label"><span className="view-color-chip" style={{ backgroundColor: swatch }} aria-hidden="true" />{triggerText}</span><ChevronRightIcon aria-hidden="true" />
  </button>;
  const body: ReactNode = <>
    <header className="view-source-header">
      {phone ? <Dialog.Title>Light color</Dialog.Title> : <h2 id={headingId}>Light color</h2>}
      <button type="button" className="view-source-close" aria-label="Close color picker" onClick={() => setOpen(false)}><XIcon aria-hidden="true" /></button>
    </header>
    {phone ? <Dialog.Description className="view-source-description">{name}</Dialog.Description> : <p className="view-source-description" id={descriptionId}>{name}</p>}
    {both && <div className="view-color-tabs" role="group" aria-label="Light color mode">
      <button type="button" aria-pressed={selected === "white"} onClick={() => setMode("white")}>White</button>
      <button type="button" aria-pressed={selected === "color"} onClick={() => setMode("color")}>Color</button>
    </div>}
    {selected === "white" && controls.temperature && <Temperature key={controls.temperature.entity.id} control={controls.temperature} />}
    {selected === "color" && controls.color && <Color key={controls.color.entity.id} control={controls.color} />}
    <div className="view-color-observed"><span>Observed</span><span>{observed ?? "No color observed"}</span></div>
    <Feedback controls={controls} />
  </>;
  return <>
    {phone ? <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger asChild>{trigger}</Dialog.Trigger>
      <Dialog.Portal><Dialog.Overlay className="view-source-backdrop" />
        <Dialog.Content className="view-source view-source-sheet view-color-picker" aria-modal="true">{body}</Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root> : <Popover.Root open={open} onOpenChange={changeOpen}>
      <Popover.Trigger asChild>{trigger}</Popover.Trigger>
      <Popover.Portal><Popover.Content className="view-source view-color-picker" align="end" sideOffset={8} collisionPadding={12}
        aria-labelledby={headingId} aria-describedby={descriptionId}>{body}</Popover.Content></Popover.Portal>
    </Popover.Root>}
    <div aria-hidden={open || undefined}><Feedback controls={controls} /></div>
  </>;
}
