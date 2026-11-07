import type { AutomationSaveDiagnostic } from "../../api/automations";

export function SaveDiagnostics({ issues, onSelectStep }: { issues: AutomationSaveDiagnostic[]; onSelectStep?: (id: string) => void }) {
  if (!issues.length) return null;
  return <ul className="workspace-notice" aria-label="Save diagnostics">{issues.map((issue, index) => <li key={`${issue.path}:${issue.code}:${index}`}>
    <p>{issue.step_id && `Step ${issue.step_id} · `}{issue.condition_id && `Condition ${issue.condition_id} · `}{issue.message ?? issue.code} <code>{issue.path}</code>{issue.span && ` · expression bytes ${issue.span.start}–${issue.span.end}`}</p>
    {issue.step_id && onSelectStep && <button onClick={() => onSelectStep(issue.step_id!)}>Show Step {issue.step_id}</button>}
  </li>)}</ul>;
}
