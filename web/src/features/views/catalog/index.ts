import { z } from "zod";
import { stackSchema, gridSchema } from "../layout/schema";
import { headingSchema, noteSchema } from "../cards/text/schema";
import { metricSchema } from "../cards/metric/schema";
import { timeSeriesSchema } from "../cards/time-series/schema";
import { stateSchema } from "../cards/state/schema";
import { readingListSchema } from "../cards/reading-list/schema";
import { deviceSchema } from "../cards/device/schema";
import { stateTimelineSchema } from "../cards/state-timeline/schema";
import { weekPatternSchema } from "../cards/week-pattern/schema";
import { childrenSchema, elementKey, plainText } from "./primitives";

// The view catalog: the only components a definition may name. Each node shape
// is closed, so unknown props or children never reach a renderer.
export const elementSchemas = {
  Stack: z.strictObject({ type: z.literal("Stack"), props: stackSchema, children: childrenSchema }),
  Grid: z.strictObject({ type: z.literal("Grid"), props: gridSchema, children: childrenSchema }),
  Heading: z.strictObject({ type: z.literal("Heading"), props: headingSchema }),
  Note: z.strictObject({ type: z.literal("Note"), props: noteSchema }),
  Metric: z.strictObject({ type: z.literal("Metric"), props: metricSchema }),
  TimeSeries: z.strictObject({ type: z.literal("TimeSeries"), props: timeSeriesSchema }),
  State: z.strictObject({ type: z.literal("State"), props: stateSchema }),
  ReadingList: z.strictObject({ type: z.literal("ReadingList"), props: readingListSchema }),
  Device: z.strictObject({ type: z.literal("Device"), props: deviceSchema }),
  StateTimeline: z.strictObject({ type: z.literal("StateTimeline"), props: stateTimelineSchema }),
  WeekPattern: z.strictObject({ type: z.literal("WeekPattern"), props: weekPatternSchema }),
};
export const elementSchema = z.discriminatedUnion("type", [
  elementSchemas.Stack, elementSchemas.Grid, elementSchemas.Heading,
  elementSchemas.Note, elementSchemas.Metric, elementSchemas.TimeSeries,
  elementSchemas.State, elementSchemas.ReadingList, elementSchemas.Device,
  elementSchemas.StateTimeline, elementSchemas.WeekPattern,
]);
export const definitionSchema = z.strictObject({
  schema_version: z.literal(1), catalog_version: z.literal("hearth.dashboard/v1"), title: plainText(80),
  spec: z.strictObject({ root: elementKey, elements: z.record(elementKey, elementSchema) }),
});
export type ViewDefinition = z.infer<typeof definitionSchema>;
export type ViewElement = z.infer<typeof elementSchema>;
export type ViewArtifact = { kind: "hearth.view"; artifact_id: string; definition: unknown };
