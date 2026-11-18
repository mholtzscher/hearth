import { XIcon } from "lucide-react";
import {
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { CSSProperties, ReactNode } from "react";

import { Button } from "../../../components/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "../../../components/dialog";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "../../../components/popover";
import { SegmentedControl } from "../../../components/segmented-control";
import { cn } from "../../../lib/utils";
import { pointer } from "../bindings/state-fields";
import { ColorWheel } from "./color-wheel";
import {
  colorHex,
  colorHS,
  colorRGB,
  hsRGB,
  parseHex,
  readColor,
  rgbHS,
} from "./light-color";
import type {
  ColorControl,
  ColorValue,
  HS,
  LightColorControls,
  RGB,
  TemperatureControl,
} from "./light-color";
import { LightControlFeedback } from "./light-control-feedback";
import { sliderClasses, sliderLabelClasses } from "./slider-styles";
import { useColorSetting } from "./use-color-setting";

const phoneQuery = "(max-width: 720px)";
const subscribePhone = (change: () => void) => {
  const media = window.matchMedia?.(phoneQuery);
  media?.addEventListener("change", change);
  return () => media?.removeEventListener("change", change);
};
const isPhone = () => window.matchMedia?.(phoneQuery).matches ?? false;
const hintClasses = "mt-3 text-label leading-normal text-gray-8";
const exactClasses = "mt-4 flex flex-wrap items-center gap-2.5";
const labelClasses = "flex items-center gap-1.5 text-label text-gray-8";
const inputClasses =
  "min-h-11 w-[76px] min-w-0 rounded-md border border-gray-4 bg-gray-3 p-2 font-[inherit] text-gray-10 tabular-nums caret-violet disabled:cursor-not-allowed disabled:text-gray-6 focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-ring";

/**
 * The Command queue stores plain records; the exported color interfaces have no
 * implicit index signature, so the shared ControlValue constraint needs these
 * structural shapes.
 */
type ColorSettingValue =
  | { red: number; green: number; blue: number }
  | { hue: number; saturation: number };
const readTemperature = (raw: unknown) => {
  const value = typeof raw === "number" ? raw : pointer(raw, "/value");
  return typeof value === "number" && Number.isFinite(value)
    ? value
    : undefined;
};

const Range = ({
  label,
  value,
  valueText,
  minimum,
  maximum,
  step = 1,
  disabled,
  pending,
  spectrum,
  onBegin,
  onChange,
  onEnd,
  onCancel,
  onBlur,
}: {
  label: string;
  value: number;
  valueText: string;
  minimum: number;
  maximum: number;
  step?: number;
  disabled: boolean;
  pending: boolean;
  spectrum?: string;
  onBegin: () => void;
  onChange: (value: number) => void;
  onEnd: (value: number) => void;
  onCancel: () => void;
  onBlur: () => void;
}) => {
  const id = useId();
  return (
    <div className="mt-4">
      <div className={sliderLabelClasses}>
        <label htmlFor={id}>{label}</label>
        <span className="text-gray-9" aria-hidden="true">
          {valueText}
        </span>
      </div>
      <input
        id={id}
        type="range"
        className={cn(
          sliderClasses,
          "[&::-webkit-slider-thumb]:border-gray-10 [&::-moz-range-thumb]:border-gray-10 disabled:[&::-webkit-slider-thumb]:border-gray-6 disabled:[&::-moz-range-thumb]:border-gray-6 [--knob:var(--gray-2)] disabled:[--knob:var(--gray-2)] [&::-moz-range-progress]:[background:transparent] [&::-moz-range-thumb]:box-border [&::-moz-range-thumb]:border-2 [&::-moz-range-track]:h-1.5 [&::-moz-range-track]:[background:var(--spectrum)] disabled:[&::-moz-range-track]:[background:var(--gray-4)] [&::-webkit-slider-runnable-track]:h-1.5 [&::-webkit-slider-runnable-track]:[background:var(--spectrum)] disabled:[&::-webkit-slider-runnable-track]:[background:var(--gray-4)] [&::-webkit-slider-thumb]:-mt-1.5 [&::-webkit-slider-thumb]:border-2"
        )}
        value={value}
        min={minimum}
        max={maximum}
        step={step}
        aria-valuetext={valueText}
        aria-busy={pending || undefined}
        disabled={disabled}
        style={{ "--spectrum": spectrum } as CSSProperties}
        onPointerDown={onBegin}
        onPointerUp={(event) => onEnd(Number(event.currentTarget.value))}
        onPointerCancel={onCancel}
        onChange={(event) => onChange(Number(event.currentTarget.value))}
        onBlur={onBlur}
      />
    </div>
  );
};

const Temperature = ({ control }: { control: TemperatureControl }) => {
  const { entity, minimum, maximum, step } = control;
  const setting = useColorSetting({
    entity,
    read: readTemperature,
    fallback: minimum,
  });
  const [exact, setExact] = useState(() => String(setting.value));
  const [synced, setSynced] = useState(setting.value);
  // Reset the editable text whenever the setting itself moves, so a retained
  // draft cannot resurface when the value returns to an earlier reading.
  if (synced !== setting.value) {
    setSynced(setting.value);
    setExact(String(setting.value));
  }
  const id = useId();
  const {
    begin: handleBegin,
    cancel: handleCancel,
    blur: handleBlur,
  } = setting;
  const snap = (value: number) =>
    Math.max(minimum, Math.min(maximum, Math.round(value / step) * step));
  return (
    <fieldset disabled={!entity.enabled} className="m-0 min-w-0 border-0 p-0">
      <legend className="sr-only">White temperature</legend>
      {!entity.enabled && (
        <p className={hintClasses}>This Entity is disabled.</p>
      )}
      {setting.observed === undefined && (
        <p className={hintClasses}>Temperature has not been observed yet.</p>
      )}
      <Range
        label="Color temperature"
        value={-snap(setting.value)}
        valueText={`${setting.value} mired · ${Math.round(1_000_000 / setting.value).toLocaleString()} K`}
        minimum={-maximum}
        maximum={-minimum}
        step={step}
        disabled={!entity.enabled}
        pending={setting.pending}
        spectrum="linear-gradient(90deg,#ff9f43,#fff1de,#a8c8ff)"
        onBegin={handleBegin}
        onChange={(value) => setting.change(-value)}
        onEnd={(value) => setting.end(-value)}
        onCancel={handleCancel}
        onBlur={handleBlur}
      />
      <div className="text-label text-gray-8 flex justify-between gap-3">
        <span>Warm · {Math.round(1_000_000 / maximum).toLocaleString()} K</span>
        <span>Cool · {Math.round(1_000_000 / minimum).toLocaleString()} K</span>
      </div>
      <form
        className={exactClasses}
        onSubmit={(event) => {
          event.preventDefault();
          if (event.currentTarget.reportValidity()) {
            setting.commit(Number(exact));
          }
        }}
      >
        <label className={labelClasses} htmlFor={id}>
          Exact value{" "}
          <input
            className={inputClasses}
            id={id}
            type="number"
            min={minimum}
            max={maximum}
            step={step}
            required
            value={exact}
            onChange={(event) => setExact(event.target.value)}
          />{" "}
          mired
        </label>
        <Button variant="outline" size="touch" type="submit">
          Set white
        </Button>
      </form>
    </fieldset>
  );
};

const ExactColor = ({
  value,
  disabled,
  onCommit,
}: {
  value: RGB;
  disabled: boolean;
  onCommit: (value: RGB) => void;
}) => {
  const [channels, setChannels] = useState(() => [
    String(value.red),
    String(value.green),
    String(value.blue),
  ]);
  const [hex, setHex] = useState(() => colorHex(value));
  const [synced, setSynced] = useState(() => ({
    red: value.red,
    green: value.green,
    blue: value.blue,
  }));
  // Reset both exact forms whenever any channel changes, matching what the
  // observed or requested reading shows; settling on the same channels again
  // must not bring an older draft back.
  if (
    synced.red !== value.red ||
    synced.green !== value.green ||
    synced.blue !== value.blue
  ) {
    setSynced({ red: value.red, green: value.green, blue: value.blue });
    setChannels([String(value.red), String(value.green), String(value.blue)]);
    setHex(colorHex(value));
  }
  const id = useId();
  const updateChannel = (index: number, text: string) => {
    setChannels(
      channels.map((channel, channelIndex) =>
        channelIndex === index ? text : channel
      )
    );
  };
  return (
    <div>
      <form
        className={exactClasses}
        onSubmit={(event) => {
          event.preventDefault();
          if (event.currentTarget.reportValidity()) {
            onCommit({
              red: Number(channels[0]),
              green: Number(channels[1]),
              blue: Number(channels[2]),
            });
          }
        }}
      >
        <div className="flex w-full flex-wrap gap-2.5">
          {["R", "G", "B"].map((channel, index) => (
            <label
              className={labelClasses}
              key={channel}
              htmlFor={`${id}-${channel}`}
            >
              {channel}
              <input
                className={cn(inputClasses, "w-16")}
                id={`${id}-${channel}`}
                type="number"
                min={0}
                max={255}
                step={1}
                required
                disabled={disabled}
                value={channels[index]}
                onChange={(event) => updateChannel(index, event.target.value)}
              />
            </label>
          ))}
        </div>
        <Button
          variant="outline"
          size="touch"
          type="submit"
          disabled={disabled}
        >
          Set RGB
        </Button>
      </form>
      <form
        className={exactClasses}
        onSubmit={(event) => {
          event.preventDefault();
          const parsed = parseHex(hex);
          if (event.currentTarget.reportValidity() && parsed) {
            onCommit(parsed);
          }
        }}
      >
        <label className={labelClasses} htmlFor={`${id}-hex`}>
          Hex{" "}
          <input
            className={cn(inputClasses, "w-[100px]")}
            id={`${id}-hex`}
            type="text"
            pattern="#?[0-9a-fA-F]{6}"
            maxLength={7}
            required
            spellCheck={false}
            value={hex}
            disabled={disabled}
            onChange={(event) => setHex(event.target.value)}
          />
        </label>
        <Button
          variant="outline"
          size="touch"
          type="submit"
          disabled={disabled}
        >
          Set hex
        </Button>
      </form>
    </div>
  );
};

const Color = ({ control }: { control: ColorControl }) => {
  const { entity, kind } = control;
  const setting = useColorSetting<ColorSettingValue>({
    entity,
    read: (value) => readColor(value, kind),
    fallback:
      kind === "rgb"
        ? { red: 255, green: 255, blue: 255 }
        : { hue: 0, saturation: 0 },
  });
  // White has no encoded hue, and low RGB levels quantize small hue changes.
  // Retain the editing intent while its encoded RGB still matches the setting.
  const [intent, setIntent] = useState<{ hs: HS; hex: string }>();
  const [seenFailed, setSeenFailed] = useState(setting.failed);
  // A failure discards editing intent permanently until the next edit, so an
  // earlier hue cannot reappear once a later Command clears the failure.
  if (seenFailed !== setting.failed) {
    setSeenFailed(setting.failed);
    if (setting.failed) {
      setIntent(undefined);
    }
  }
  const hs =
    intent?.hex === colorHex(setting.value)
      ? intent.hs
      : colorHS(setting.value);
  const rgb = colorRGB(setting.value);
  const { begin: handleBegin, blur: handleBlur } = setting;
  const fromHS = (value: HS): ColorValue => {
    if (kind === "hs") {
      return value;
    }
    return hsRGB(value, Math.max(rgb.red, rgb.green, rgb.blue) || 255);
  };
  const previewHS = (value: HS) => {
    const encoded = fromHS(value);
    setIntent({ hs: value, hex: colorHex(encoded) });
    setting.change(encoded);
  };
  const endHS = (value: HS) => {
    const encoded = fromHS(value);
    setIntent({ hs: value, hex: colorHex(encoded) });
    setting.end(encoded);
  };
  const cancelHS = () => {
    setIntent(undefined);
    setting.cancel();
  };
  return (
    <fieldset className="m-0 min-w-0 border-0 p-0" disabled={!entity.enabled}>
      <legend className="sr-only">Light color</legend>
      {!entity.enabled && (
        <p className={hintClasses}>This Entity is disabled.</p>
      )}
      {setting.observed === undefined && (
        <p className={hintClasses}>Color has not been observed yet.</p>
      )}
      <ColorWheel
        value={hs}
        disabled={!entity.enabled}
        pending={setting.pending}
        onBegin={handleBegin}
        onChange={previewHS}
        onEnd={endHS}
        onCancel={cancelHS}
        onBlur={handleBlur}
      />
      <Range
        label="Hue"
        value={hs.hue}
        valueText={`${hs.hue}°`}
        minimum={0}
        maximum={359}
        disabled={!entity.enabled}
        pending={setting.pending}
        spectrum="linear-gradient(90deg,#f66,#ff6,#6f6,#6ff,#66f,#f6f,#f66)"
        onBegin={handleBegin}
        onChange={(hue) => previewHS({ ...hs, hue })}
        onEnd={(hue) => endHS({ ...hs, hue })}
        onCancel={cancelHS}
        onBlur={handleBlur}
      />
      <Range
        label="Saturation"
        value={hs.saturation}
        valueText={`${hs.saturation} %`}
        minimum={0}
        maximum={100}
        disabled={!entity.enabled}
        pending={setting.pending}
        spectrum={`linear-gradient(90deg,#fff,hsl(${hs.hue} 100% 50%))`}
        onBegin={handleBegin}
        onChange={(saturation) => previewHS({ ...hs, saturation })}
        onEnd={(saturation) => endHS({ ...hs, saturation })}
        onCancel={cancelHS}
        onBlur={handleBlur}
      />
      <ExactColor
        value={rgb}
        disabled={!entity.enabled}
        onCommit={(next) => {
          setIntent(undefined);
          setting.commit(kind === "rgb" ? next : rgbHS(next));
        }}
      />
      <p className={hintClasses}>
        {kind === "hs"
          ? "This light stores hue and saturation. RGB is converted; brightness stays unchanged."
          : "Exact RGB sets the color. Brightness is controlled separately."}
      </p>
    </fieldset>
  );
};

const initialMode = (
  activeId: string | undefined,
  colorId: string | undefined,
  hasTemperature: boolean
): "white" | "color" => {
  if (colorId === activeId) {
    return "color";
  }
  return hasTemperature ? "white" : "color";
};
const selectedMode = (
  mode: "white" | "color",
  controls: LightColorControls
): "white" | "color" => {
  if (mode === "white" && controls.temperature) {
    return "white";
  }
  return controls.color ? "color" : "white";
};
const triggerLabel = (controls: LightColorControls) => {
  if (controls.temperature && controls.color) {
    return "Adjust white or color";
  }
  return controls.temperature ? "Adjust white" : "Adjust color";
};
const colorTrigger = ({
  name,
  text,
  open,
  inlineFeedback,
  feedbackId,
}: {
  name: string;
  text: string;
  open: boolean;
  inlineFeedback: boolean;
  feedbackId: string;
}) => (
  <button
    type="button"
    className="hover:bg-gray-2 focus-visible:outline-ring relative grid size-7 shrink-0 cursor-pointer place-items-center rounded-full p-0 before:absolute before:-inset-2 before:rounded-md focus-visible:outline-2 focus-visible:outline-offset-3"
    aria-label={`${name}: ${text}`}
    title={text}
    aria-expanded={open}
    aria-describedby={inlineFeedback ? feedbackId : undefined}
  >
    <span
      data-slot="color-wheel-icon"
      className="border-gray-8 after:bg-gray-10 relative block size-5 rounded-full border [background:conic-gradient(#ff4848,#ffd54a,#62d67c,#50d8ef,#8581ff,#ef70cb,#ff4848)] after:absolute after:inset-1.5 after:rounded-full"
      aria-hidden="true"
    />
  </button>
);

const PickerBody = ({
  controls,
  name,
  observed,
  phone,
  mode,
  onModeChange,
  onClose,
  headingId,
  descriptionId,
  brightnessId,
  powerId,
}: {
  controls: LightColorControls;
  name: string;
  observed?: string;
  phone: boolean;
  mode: "white" | "color";
  onModeChange: (mode: "white" | "color") => void;
  onClose: () => void;
  headingId: string;
  descriptionId: string;
  brightnessId?: string;
  powerId?: string;
}) => {
  const selected = selectedMode(mode, controls);
  const both = !!controls.temperature && !!controls.color;
  return (
    <>
      <header
        className={cn(
          "flex items-start justify-between gap-5",
          phone && "bg-gray-2 sticky top-[-22px] z-1 pt-1.5"
        )}
      >
        {phone ? (
          <DialogTitle>Light color</DialogTitle>
        ) : (
          <h2 className="text-title leading-[1.4] font-semibold" id={headingId}>
            Light color
          </h2>
        )}
        <Button
          variant="ghost"
          size="icon-touch"
          className="-mt-2.5 -mr-2.5"
          type="button"
          aria-label="Close color picker"
          onClick={onClose}
        >
          <XIcon aria-hidden="true" />
        </Button>
      </header>
      {phone ? (
        <DialogDescription className="text-gray-8 mt-1.5 mb-[22px]">
          {name}
        </DialogDescription>
      ) : (
        <p className="text-gray-8 mt-1.5 mb-[22px]" id={descriptionId}>
          {name}
        </p>
      )}
      {both && (
        <div className="mb-5">
          <SegmentedControl
            label="Light color mode"
            size="touch"
            value={selected}
            onValueChange={onModeChange}
            options={[
              { value: "white", label: "White" },
              { value: "color", label: "Color" },
            ]}
          />
        </div>
      )}
      {selected === "white" && controls.temperature && (
        <Temperature
          key={controls.temperature.entity.id}
          control={controls.temperature}
        />
      )}
      {selected === "color" && controls.color && (
        <Color key={controls.color.entity.id} control={controls.color} />
      )}
      <div className="border-gray-4 text-label text-gray-8 mt-5 flex flex-wrap justify-between gap-x-4 gap-y-2 border-t pt-4">
        <span>Observed</span>
        <span className="text-gray-10 tabular-nums">
          {observed ?? "No color observed"}
        </span>
      </div>
      <LightControlFeedback
        controls={controls}
        brightnessId={brightnessId}
        powerId={powerId}
        detailed
      />
    </>
  );
};

/** Capability-derived color control; the queue and observed reading outlive the overlay. */
export const LightColorPicker = ({
  controls,
  activeId,
  name,
  observed,
  open,
  onOpenChange,
  brightnessId,
  powerId,
  inlineFeedback = false,
}: {
  controls: LightColorControls;
  activeId?: string;
  name: string;
  observed?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  brightnessId?: string;
  powerId?: string;
  inlineFeedback?: boolean;
}) => {
  const phone = useSyncExternalStore(subscribePhone, isPhone);
  const [mode, setMode] = useState<"white" | "color">("white");
  const wasOpen = useRef(false);
  const colorId = controls.color?.entity.id;
  const hasTemperature = controls.temperature !== undefined;
  useEffect(() => {
    if (open && !wasOpen.current) {
      setMode(initialMode(activeId, colorId, hasTemperature));
    }
    wasOpen.current = open;
  }, [open, activeId, colorId, hasTemperature]);
  const headingId = useId();
  const descriptionId = useId();
  const feedbackId = useId();
  if (!controls.temperature && !controls.color) {
    return null;
  }
  const body: ReactNode = (
    <PickerBody
      controls={controls}
      name={name}
      observed={observed}
      phone={phone}
      mode={mode}
      onModeChange={setMode}
      onClose={() => onOpenChange(false)}
      headingId={headingId}
      descriptionId={descriptionId}
      brightnessId={brightnessId}
      powerId={powerId}
    />
  );
  const trigger = colorTrigger({
    name,
    text: triggerLabel(controls),
    open,
    inlineFeedback,
    feedbackId,
  });
  return (
    <span
      className={cn(
        "relative -ms-1.5 h-7 w-7 shrink-0",
        inlineFeedback && "inline-flex w-auto items-center gap-1"
      )}
    >
      {phone ? (
        <Dialog open={open} onOpenChange={onOpenChange}>
          <DialogTrigger asChild>{trigger}</DialogTrigger>
          <DialogContent
            placement="bottom"
            data-placement="bottom"
            aria-modal="true"
          >
            {body}
          </DialogContent>
        </Dialog>
      ) : (
        <Popover open={open} onOpenChange={onOpenChange}>
          <PopoverTrigger asChild>{trigger}</PopoverTrigger>
          <PopoverContent
            className="block w-[min(370px,calc(100vw-24px))]"
            align="end"
            sideOffset={8}
            collisionPadding={12}
            aria-labelledby={headingId}
            aria-describedby={descriptionId}
          >
            {body}
          </PopoverContent>
        </Popover>
      )}
      {inlineFeedback && (
        <LightControlFeedback
          controls={controls}
          inline
          hidden={open}
          descriptionId={feedbackId}
        />
      )}
    </span>
  );
};
