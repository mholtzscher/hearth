import type { ReactNode } from "react";

import { Distribution } from "./distribution";
import { HeatGrid } from "./heat-grid";
import { PeriodBars } from "./period-bars";
import { PeriodComparison } from "./period-comparison";
import { RankedReadings } from "./ranked-readings";
import { SteppedLine } from "./stepped-line";

const Pair = ({
  question,
  children,
}: {
  question: string;
  children: ReactNode;
}) => (
  <section>
    <h3 className="text-title mb-3">{question}</h3>
    <div className="grid items-start gap-3 min-[900px]:grid-cols-2">
      {children}
    </div>
  </section>
);

/**
 * Prototype review sheet: six chart forms the catalog cannot draw yet, each in
 * two takes over the same synthetic data.
 */
export const ChartPrototypes = () => (
  <div className="mx-auto grid max-w-[1120px] gap-10 p-5 min-[900px]:p-7">
    <Pair question="How much per day?">
      <PeriodBars take="columns" />
      <PeriodBars take="ledger" />
    </Pair>
    <Pair question="Is today unusual?">
      <PeriodComparison take="yesterday" />
      <PeriodComparison take="typical" />
    </Pair>
    <Pair question="Which one is highest?">
      <RankedReadings take="bars" />
      <RankedReadings take="range" />
    </Pair>
    <Pair question="When did it change?">
      <SteppedLine take="risers" />
      <SteppedLine take="holds" />
    </Pair>
    <Pair question="When is it usually warm?">
      <HeatGrid take="hours" />
      <HeatGrid take="blocks" />
    </Pair>
    <Pair question="How long was it in each range?">
      <Distribution take="histogram" />
      <Distribution take="bands" />
    </Pair>
  </div>
);
