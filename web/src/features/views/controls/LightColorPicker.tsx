import { useEffect, useId, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import { XIcon } from "lucide-react";
import { Button } from "../../../components/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "../../../components/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "../../../components/popover";
import { SegmentedControl } from "../../../components/segmented-control";
import { sliderClasses, sliderLabelClasses } from "./slider-styles";
import { cn } from "../../../lib/utils";
import { pointer } from "../bindings/state-fields";
import { LightControlFeedback } from "./LightControlFeedback";
import { ColorWheel } from "./ColorWheel";
import { useColorSetting } from "./useColorSetting";
import { colorHex, colorHS, colorRGB, hsRGB, parseHex, readColor, rgbHS, type ColorControl, type ColorValue, type HS, type LightColorControls, type RGB, type TemperatureControl } from "./light-color";

const phoneQuery = "(max-width: 720px)";
function subscribePhone(change: () => void) {
  const media = window.matchMedia?.(phoneQuery);
  media?.addEventListener("change", change);
  return () => media?.removeEventListener("change", change);
}
const isPhone = () => window.matchMedia?.(phoneQuery).matches ?? false;
const hintClasses = "mt-3 text-label leading-normal text-gray-8";
const exactClasses = "mt-4 flex flex-wrap items-center gap-2.5";
const labelClasses = "flex items-center gap-1.5 text-label text-gray-8";
const inputClasses = "min-h-11 w-[76px] min-w-0 rounded-sm border border-gray-4 bg-gray-3 p-2 font-[inherit] text-gray-10 tabular-nums caret-violet disabled:cursor-not-allowed disabled:text-gray-6 focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-ring";

function Range({ label, value, valueText, minimum, maximum, step = 1, disabled, pending, spectrum, onBegin, onChange, onEnd, onCancel, onBlur }: {
  label: string; value: number; valueText: string; minimum: number; maximum: number; step?: number;
  disabled: boolean; pending: boolean; spectrum?: string;
  onBegin: () => void; onChange: (value: number) => void; onEnd: (value: number) => void; onCancel: () => void; onBlur: () => void;
}) {
  const id = useId();
  return <div className="mt-4">
    <div className={sliderLabelClasses}><label htmlFor={id}>{label}</label><span className="text-gray-9" aria-hidden="true">{valueText}</span></div>
    <input id={id} type="range" className={cn(sliderClasses, "[--knob:var(--gray-2)] disabled:[--knob:var(--gray-2)] [&::-webkit-slider-runnable-track]:h-1.5 [&::-webkit-slider-runnable-track]:[background:var(--spectrum)] [&::-moz-range-track]:h-1.5 [&::-moz-range-track]:[background:var(--spectrum)] [&::-moz-range-progress]:[background:transparent] [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-gray-10 [&::-moz-range-thumb]:box-border [&::-moz-range-thumb]:border-2 [&::-moz-range-thumb]:border-gray-10 disabled:[&::-webkit-slider-runnable-track]:[background:var(--gray-4)] disabled:[&::-moz-range-track]:[background:var(--gray-4)] disabled:[&::-webkit-slider-thumb]:border-gray-6 disabled:[&::-moz-range-thumb]:border-gray-6")} value={value} min={minimum} max={maximum} step={step}
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
  return <fieldset disabled={!entity.enabled} className="m-0 min-w-0 border-0 p-0">
    <legend className="sr-only">White temperature</legend>
    {!entity.enabled && <p className={hintClasses}>This Entity is disabled.</p>}
    {setting.observed === undefined && <p className={hintClasses}>Temperature has not been observed yet.</p>}
    <Range label="Color temperature" value={-snap(setting.value)} valueText={`${setting.value} mired · ${Math.round(1_000_000 / setting.value).toLocaleString()} K`}
      minimum={-maximum} maximum={-minimum} step={step} disabled={!entity.enabled} pending={setting.pending}
      spectrum="linear-gradient(90deg,#ff9f43,#fff1de,#a8c8ff)"
      onBegin={setting.begin} onChange={value => setting.change(-value)} onEnd={value => setting.end(-value)} onCancel={setting.cancel} onBlur={setting.blur} />
    <div className="flex justify-between gap-3 text-label text-gray-8"><span>Warm · {Math.round(1_000_000 / maximum).toLocaleString()} K</span><span>Cool · {Math.round(1_000_000 / minimum).toLocaleString()} K</span></div>
    <form className={exactClasses} onSubmit={event => {
      event.preventDefault();
      if (event.currentTarget.reportValidity()) setting.commit(Number(exact));
    }}>
      <label className={labelClasses} htmlFor={id}>Exact value <input className={inputClasses} id={id} type="number" min={minimum} max={maximum} step={step} required value={exact} onChange={event => setExact(event.target.value)} /> mired</label>
      <Button variant="outline" size="touch" type="submit">Set white</Button>
    </form>
  </fieldset>;
}

function ExactColor({ value, disabled, onCommit }: { value: RGB; disabled: boolean; onCommit: (value: RGB) => void }) {
  const [channels, setChannels] = useState([String(value.red), String(value.green), String(value.blue)]);
  const [hex, setHex] = useState(colorHex(value));
  const id = useId();
  useEffect(() => { setChannels([String(value.red), String(value.green), String(value.blue)]); setHex(colorHex(value)); }, [value.red, value.green, value.blue]);
  return <div>
    <form className={exactClasses} onSubmit={event => {
      event.preventDefault();
      if (event.currentTarget.reportValidity()) onCommit({ red: Number(channels[0]), green: Number(channels[1]), blue: Number(channels[2]) });
    }}>
      <div className="flex w-full flex-wrap gap-2.5">{["R", "G", "B"].map((channel, index) => <label className={labelClasses} key={channel} htmlFor={`${id}-${channel}`}>{channel}
        <input className={cn(inputClasses, "w-16")} id={`${id}-${channel}`} type="number" min={0} max={255} step={1} required disabled={disabled}
          value={channels[index]} onChange={event => setChannels(channels.map((value, i) => i === index ? event.target.value : value))} />
      </label>)}</div>
      <Button variant="outline" size="touch" type="submit" disabled={disabled}>Set RGB</Button>
    </form>
    <form className={exactClasses} onSubmit={event => {
      event.preventDefault();
      const parsed = parseHex(hex);
      if (event.currentTarget.reportValidity() && parsed) onCommit(parsed);
    }}>
      <label className={labelClasses} htmlFor={`${id}-hex`}>Hex <input className={cn(inputClasses, "w-[100px]")} id={`${id}-hex`} type="text" pattern="#?[0-9a-fA-F]{6}" maxLength={7} required spellCheck={false}
        value={hex} disabled={disabled} onChange={event => setHex(event.target.value)} /></label>
      <Button variant="outline" size="touch" type="submit" disabled={disabled}>Set hex</Button>
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
  return <fieldset className="m-0 min-w-0 border-0 p-0" disabled={!entity.enabled}>
    <legend className="sr-only">Light color</legend>
    {!entity.enabled && <p className={hintClasses}>This Entity is disabled.</p>}
    {setting.observed === undefined && <p className={hintClasses}>Color has not been observed yet.</p>}
    <ColorWheel value={hs} disabled={!entity.enabled} pending={setting.pending}
      onBegin={setting.begin} onChange={previewHS} onEnd={endHS} onCancel={cancelHS} onBlur={setting.blur} />
    <Range label="Hue" value={hs.hue} valueText={`${hs.hue}°`} minimum={0} maximum={359} disabled={!entity.enabled} pending={setting.pending}
      spectrum="linear-gradient(90deg,#f66,#ff6,#6f6,#6ff,#66f,#f6f,#f66)"
      onBegin={setting.begin} onChange={hue => previewHS({ ...hs, hue })} onEnd={hue => endHS({ ...hs, hue })} onCancel={cancelHS} onBlur={setting.blur} />
    <Range label="Saturation" value={hs.saturation} valueText={`${hs.saturation} %`} minimum={0} maximum={100} disabled={!entity.enabled} pending={setting.pending}
      spectrum={`linear-gradient(90deg,#fff,hsl(${hs.hue} 100% 50%))`}
      onBegin={setting.begin} onChange={saturation => previewHS({ ...hs, saturation })} onEnd={saturation => endHS({ ...hs, saturation })} onCancel={cancelHS} onBlur={setting.blur} />
    <ExactColor value={rgb} disabled={!entity.enabled} onCommit={rgb => { setIntent(undefined); setting.commit(kind === "rgb" ? rgb : rgbHS(rgb)); }} />
    <p className={hintClasses}>{kind === "hs" ? "This light stores hue and saturation. RGB is converted; brightness stays unchanged." : "Exact RGB sets the color. Brightness is controlled separately."}</p>
  </fieldset>;
}

/** Capability-derived color control; the queue and observed reading outlive the overlay. */
export function LightColorPicker({ controls, activeId, name, observed, open, onOpenChange, brightnessId, powerId, inlineFeedback = false }: {
  controls: LightColorControls; activeId?: string; name: string; observed?: string;
  open: boolean; onOpenChange: (open: boolean) => void; brightnessId?: string; powerId?: string;
  inlineFeedback?: boolean;
}) {
  const phone = useSyncExternalStore(subscribePhone, isPhone);
  const [mode, setMode] = useState<"white" | "color">("white");
  const wasOpen = useRef(false);
  useEffect(() => {
    if (open && !wasOpen.current) setMode(controls.color?.entity.id === activeId ? "color" : controls.temperature ? "white" : "color");
    wasOpen.current = open;
  }, [open, activeId, controls.color?.entity.id, controls.temperature]);
  const headingId = useId(), descriptionId = useId(), feedbackId = useId();
  if (!controls.temperature && !controls.color) return null;
  const selected = mode === "white" && controls.temperature ? "white" : controls.color ? "color" : "white";
  const both = !!controls.temperature && !!controls.color;
  const triggerText = both ? "Adjust white or color" : controls.temperature ? "Adjust white" : "Adjust color";
  const trigger = <button type="button" className="relative grid size-7 shrink-0 cursor-pointer place-items-center rounded-full p-0 before:absolute before:-inset-2 before:rounded-md hover:bg-gray-2 focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-ring" aria-label={`${name}: ${triggerText}`} title={triggerText} aria-expanded={open}
    aria-describedby={inlineFeedback ? feedbackId : undefined}>
    <span data-slot="color-wheel-icon" className="relative block size-5 rounded-full border border-gray-8 [background:conic-gradient(#ff4848,#ffd54a,#62d67c,#50d8ef,#8581ff,#ef70cb,#ff4848)] after:absolute after:inset-1.5 after:rounded-full after:bg-gray-10" aria-hidden="true" />
  </button>;
  const body: ReactNode = <>
    <header className={cn("flex items-start justify-between gap-5", phone && "sticky top-[-22px] z-1 bg-gray-2 pt-1.5")}>
      {phone ? <DialogTitle className="text-title leading-[1.4] font-semibold">Light color</DialogTitle> : <h2 className="text-title leading-[1.4] font-semibold" id={headingId}>Light color</h2>}
      <Button variant="ghost" size="icon-touch" className="-mt-2.5 -mr-2.5" type="button" aria-label="Close color picker" onClick={() => onOpenChange(false)}><XIcon aria-hidden="true" /></Button>
    </header>
    {phone ? <DialogDescription className="mt-1.5 mb-[22px] text-gray-8">{name}</DialogDescription> : <p className="mt-1.5 mb-[22px] text-gray-8" id={descriptionId}>{name}</p>}
    {both && <div className="mb-5"><SegmentedControl label="Light color mode" size="touch" value={selected} onValueChange={setMode} options={[{ value: "white", label: "White" }, { value: "color", label: "Color" }]} /></div>}
    {selected === "white" && controls.temperature && <Temperature key={controls.temperature.entity.id} control={controls.temperature} />}
    {selected === "color" && controls.color && <Color key={controls.color.entity.id} control={controls.color} />}
    <div className="mt-5 flex flex-wrap justify-between gap-x-4 gap-y-2 border-t border-gray-4 pt-4 text-label text-gray-8"><span>Observed</span><span className="text-gray-10 tabular-nums">{observed ?? "No color observed"}</span></div>
    <LightControlFeedback controls={controls} brightnessId={brightnessId} powerId={powerId} detailed />
  </>;
  return <span className={cn("relative -ms-1.5 h-7 w-7 shrink-0", inlineFeedback && "inline-flex w-auto items-center gap-1")}>
    {phone ? <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent placement="bottom" data-placement="bottom" aria-modal="true">{body}</DialogContent>
    </Dialog> : <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent className="block w-[min(370px,calc(100vw-24px))]" align="end" sideOffset={8} collisionPadding={12}
        aria-labelledby={headingId} aria-describedby={descriptionId}>{body}</PopoverContent>
    </Popover>}
    {inlineFeedback && <LightControlFeedback controls={controls} inline hidden={open} descriptionId={feedbackId} />}
  </span>;
}
