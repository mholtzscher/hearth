import { Highlight, themes } from "prism-react-renderer";

/** Syntax-highlighted JSON block. Token colors from nightOwl, transparent
    background so the block blends with the surrounding surface. */
const jsonTheme = {
  ...themes.nightOwl,
  plain: { ...themes.nightOwl.plain, backgroundColor: "transparent" },
};

export const JsonCode = ({ code }: { code: string }) => (
  <Highlight code={code} language="json" theme={jsonTheme}>
    {({ tokens, getLineProps, getTokenProps }) => (
      <pre className="text-label overflow-auto">
        {/* getLineProps/getTokenProps echo every extra input key back into
              the props they return, so ask for line and token props only and
              pass the React key explicitly: spreading a key into JSX warns. */}
        {tokens.map((line, lineIndex) => (
          <div key={lineIndex} {...getLineProps({ line })}>
            {line.map((token, tokenIndex) => (
              <span key={tokenIndex} {...getTokenProps({ token })} />
            ))}
          </div>
        ))}
      </pre>
    )}
  </Highlight>
);
