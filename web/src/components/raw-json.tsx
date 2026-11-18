import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "./accordion.tsx";
import { JsonCode } from "./json-code.tsx";

export const RawJson = ({
  value,
  title = "Raw JSON",
  code,
  headingLevel = 3,
}: {
  value: unknown;
  title?: string;
  code?: string;
  headingLevel?: 2 | 3 | 4 | 5 | 6;
}) => (
  <Accordion
    type="single"
    collapsible
    className="rounded-card mt-4 border px-3"
  >
    <AccordionItem value="raw-json">
      <AccordionTrigger
        headingLevel={headingLevel}
        className="text-sm font-medium"
      >
        {title}
      </AccordionTrigger>
      <AccordionContent>
        <JsonCode
          code={code ?? JSON.stringify(value, null, 2) ?? String(value)}
        />
      </AccordionContent>
    </AccordionItem>
  </Accordion>
);
