import { Highlight } from "prism-react-renderer";
import type { PrismTheme } from "prism-react-renderer";

/** Prism tokens take Hearth's palette rather than a bundled theme. */
const unstyled: PrismTheme = { plain: {}, styles: [] };
const tokenStyles =
  "[&_.token.property]:text-gray-9 [&_.token.string]:text-chart-5 [&_.token.number]:text-chart-7 [&_.token.boolean]:text-chart-3 [&_.token.null]:text-chart-3 [&_.token.punctuation]:text-muted-foreground [&_.token.operator]:text-muted-foreground";
const chip =
  "rounded-xs border border-border bg-gray-2 px-[5px] py-px text-[.85em] text-gray-9 [overflow-wrap:anywhere] [box-decoration-break:clone]";

/** Exact JSON source, highlighted. Inline values sit in a code chip within
    the sentence; the block form shows a whole definition. */
export const JsonCode = ({
  source,
  block = false,
}: {
  source: string;
  block?: boolean;
}) => (
  <Highlight code={source} language="json" theme={unstyled}>
    {({ tokens, getTokenProps }) => {
      // getTokenProps echoes extra input keys, so pass the React key separately.
      const lines = tokens.map((line, lineIndex) => (
        <span key={lineIndex}>
          {lineIndex > 0 && "\n"}
          {line.map((token, tokenIndex) => (
            <span key={tokenIndex} {...getTokenProps({ token })} />
          ))}
        </span>
      ));
      return block ? (
        <pre
          data-slot="automation-code-block"
          className={`text-caption text-gray-9 mb-2 [overflow-wrap:anywhere] whitespace-pre-wrap ${tokenStyles}`}
        >
          <code>{lines}</code>
        </pre>
      ) : (
        <code data-slot="automation-code" className={`${chip} ${tokenStyles}`}>
          {lines}
        </code>
      );
    }}
  </Highlight>
);

/** An Automation expression: code, but not JSON, so it takes no token colors. */
export const ExpressionCode = ({ source }: { source: string }) => (
  <code data-slot="automation-code" className={chip}>
    {source}
  </code>
);
