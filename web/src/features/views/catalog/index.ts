import { defineCatalog } from "@json-render/core";
import { schema } from "@json-render/react/schema";
import { z } from "zod";
import { stackSchema, gridSchema } from "../layout/schema";
import { headingSchema, noteSchema } from "../cards/text/schema";
import { metricSchema } from "../cards/metric/schema";
import { timeSeriesSchema } from "../cards/time-series/schema";
import { childrenSchema, elementKey, plainText } from "./primitives";

export const catalog = defineCatalog(schema, {
  components: {
    Stack: { props: stackSchema, slots: ["default"], description: "Vertical layout" },
    Grid: { props: gridSchema, slots: ["default"], description: "Responsive grid" },
    Heading: { props: headingSchema, description: "Plain text heading" },
    Note: { props: noteSchema, description: "Plain text explanation" },
    Metric: { props: metricSchema, description: "Live numeric State field; absent optional fields show No value" },
    TimeSeries: { props: timeSeriesSchema, description: "Retained State field sample min/max/mean; absent reports make bucket gaps, not time-weighted or circular means" },
  },
  actions: {},
});

// The renderer's general-purpose schema permits expressions and loose props.
// Derive the persisted union from the same catalog, closing each node shape.
const components = catalog.data.components;
export const elementSchemas = {
  Stack: z.strictObject({ type: z.literal("Stack"), props: components.Stack.props, children: childrenSchema }),
  Grid: z.strictObject({ type: z.literal("Grid"), props: components.Grid.props, children: childrenSchema }),
  Heading: z.strictObject({ type: z.literal("Heading"), props: components.Heading.props }),
  Note: z.strictObject({ type: z.literal("Note"), props: components.Note.props }),
  Metric: z.strictObject({ type: z.literal("Metric"), props: components.Metric.props }),
  TimeSeries: z.strictObject({ type: z.literal("TimeSeries"), props: components.TimeSeries.props }),
};
export const elementSchema = z.discriminatedUnion("type", [
  elementSchemas.Stack, elementSchemas.Grid, elementSchemas.Heading,
  elementSchemas.Note, elementSchemas.Metric, elementSchemas.TimeSeries,
]);
export const definitionSchema = z.strictObject({
  schema_version: z.literal(1), catalog_version: z.literal("hearth.dashboard/v1"), title: plainText(80),
  spec: z.strictObject({ root: elementKey, elements: z.record(elementKey, elementSchema) }),
});
export type ViewDefinition = z.infer<typeof definitionSchema>;
export type ViewElement = z.infer<typeof elementSchema>;
export type ViewArtifact = { kind: "hearth.view"; artifact_id: string; definition: unknown };
