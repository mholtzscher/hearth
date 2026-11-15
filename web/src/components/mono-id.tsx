/** Long opaque IDs: monospace, truncated to the column, full value on hover. */
export function MonoId({ value, className = "" }: { value: string; className?: string }) {
  return (
    <span title={value} className={`block truncate font-mono text-xs ${className}`}>
      {value}
    </span>
  );
}
