/** Long opaque IDs: monospace, truncated to the column, full value on hover. */
export const MonoId = ({
  value,
  className = "",
}: {
  value: string;
  className?: string;
}) => (
  <span
    title={value}
    className={`text-caption block truncate font-mono ${className}`}
  >
    {value}
  </span>
);
