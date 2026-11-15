import type { AutomationSaveDiagnostic } from "../../api/automations";
import { Button } from "../../components/button";
import { StatusNotice } from "../../components/notice";

export const SaveDiagnostics = ({
  issues,
  onSelectStep,
}: {
  issues: AutomationSaveDiagnostic[];
  onSelectStep?: (id: string) => void;
}) => {
  if (!issues.length) {
    return null;
  }
  return (
    <StatusNotice>
      <ul aria-label="Save diagnostics">
        {issues.map((issue, index) => (
          <li key={`${issue.path}:${issue.code}:${index}`}>
            <p>
              {issue.step_id && `Step ${issue.step_id} · `}
              {issue.condition_id && `Condition ${issue.condition_id} · `}
              {issue.message ?? issue.code} <code>{issue.path}</code>
              {issue.span &&
                ` · expression bytes ${issue.span.start}–${issue.span.end}`}
            </p>
            {issue.step_id && onSelectStep && (
              <Button
                variant="outline"
                size="touch"
                className="m-1.5"
                onClick={() => {
                  if (issue.step_id) {
                    onSelectStep(issue.step_id);
                  }
                }}
              >
                Show Step {issue.step_id}
              </Button>
            )}
          </li>
        ))}
      </ul>
    </StatusNotice>
  );
};
