import { useState } from "react";

import type { MetricData } from "../../bindings/types";

interface Sample {
  text: string;
  at: number;
  unit: string | undefined;
  source: string | undefined;
  value: number | undefined;
  eligible: boolean;
}

const changedDigit = (previous: Sample, next: Sample) => {
  if (
    !next.eligible ||
    !previous.eligible ||
    next.at <= previous.at ||
    !Number.isFinite(next.at) ||
    !Number.isFinite(previous.at) ||
    next.unit !== previous.unit ||
    next.source !== previous.source ||
    next.text.length !== previous.text.length
  ) {
    return;
  }
  const differences = [...next.text].flatMap((digit, index) =>
    digit === previous.text[index] ? [] : [index]
  );
  const [index] = differences;
  if (
    differences.length === 1 &&
    /\d/u.test(next.text[index]) &&
    /\d/u.test(previous.text[index])
  ) {
    return index;
  }
};

/** Roll the incoming glyph only. The outgoing value is never drawn over it. */
export const MetricNumber = ({
  reading,
  eligible,
  source,
}: {
  reading?: MetricData;
  eligible: boolean;
  source?: string;
}) => {
  const sample = {
    text: reading?.value.toFixed(reading.format.fraction_digits) ?? "—",
    at: Date.parse(reading?.observed_at ?? ""),
    unit: reading?.format.unit_label,
    value: reading?.value,
    eligible,
    source,
  };
  const key = JSON.stringify(sample);
  const [previous, setPrevious] = useState({
    key,
    sample,
    roll: undefined as number | undefined,
    rising: false,
  });
  if (previous.key !== key) {
    setPrevious({
      key,
      sample,
      roll: changedDigit(previous.sample, sample),
      rising: (sample.value ?? 0) > (previous.sample.value ?? 0),
    });
  }
  const { roll } = previous;
  if (roll === undefined) {
    return sample.text;
  }
  return (
    <>
      <span className="sr-only motion-reduce:not-sr-only">{sample.text}</span>
      <span aria-hidden="true" className="motion-reduce:hidden">
        {sample.text.slice(0, roll)}
        <span className="inline-block overflow-clip align-bottom">
          <span
            key={key}
            data-slot="rolling-digit"
            className={
              previous.rising
                ? "animate-roll block [--roll-distance:100%]"
                : "animate-roll block [--roll-distance:-100%]"
            }
            onAnimationEnd={() =>
              setPrevious((current) =>
                current.key === key ? { ...current, roll: undefined } : current
              )
            }
          >
            {sample.text[roll]}
          </span>
        </span>
        {sample.text.slice(roll + 1)}
      </span>
    </>
  );
};
