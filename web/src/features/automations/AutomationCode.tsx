import { Highlight, type PrismTheme } from "prism-react-renderer";

/** Token colors come from automations.css through Prism's token classes, so
    the palette stays on Hearth's design tokens rather than a bundled theme. */
const unstyled: PrismTheme = { plain: {}, styles: [] };

/** Exact JSON source, highlighted. Inline values sit in a code chip within
    the sentence; the block form shows a whole definition. */
export function JsonCode({ source, block = false }: { source: string; block?: boolean }) {
  return <Highlight code={source} language="json" theme={unstyled}>
    {({ tokens, getTokenProps }) => {
      // getTokenProps echoes extra input keys, so pass the React key separately.
      const lines = tokens.map((line, lineIndex) => <span key={lineIndex}>{lineIndex > 0 && "\n"}{line.map((token, tokenIndex) => <span key={tokenIndex} {...getTokenProps({ token })}/>)}</span>);
      return block ? <pre className="automation-code-block"><code>{lines}</code></pre> : <code className="automation-code">{lines}</code>;
    }}
  </Highlight>;
}

/** An Automation expression: code, but not JSON, so it takes no token colors. */
export function ExpressionCode({ source }: { source: string }) {
  return <code className="automation-code">{source}</code>;
}
