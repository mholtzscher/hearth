# Merge-risk Flue prototype (disposable)

Contained prototype answering: can a Flue `flue run` agent apply the vendored
`merge-risk` skill in GitHub Actions on PR open and post the result?

All files live in this one dir (`prototype-merge-risk/`). Nothing is wired into
`.github/workflows/` or the repo root; the workflow here is a DRAFT copy.

## Contents

- `src/agents/merge-risk.ts` — agent: model + `local({env:{GH_TOKEN}})` sandbox,
  instruction to apply the `merge-risk` skill and emit comment-only markdown.
- `.agents/skills/merge-risk/SKILL.md` — verbatim copy from
  `warpdotdev/common-skills` (source URL in frontmatter).
- `github-workflow.merge-risk-prototype.yml` — DRAFT workflow (`pull_request:
  [opened, synchronize, reopened]`, `pull-requests: write`); copy to
  `.github/workflows/` only to activate.
- `package.json` — deps `@flue/runtime`, `valibot`, dev `@flue/cli`.
- `AGENTS.md` — minimal prototype context.

## Try (needs OPENCODE_API_KEY + GH_TOKEN in env; no secrets post anything)

```sh
cd prototype-merge-risk
npm install
OPENCODE_API_KEY=... GH_TOKEN=... npx flue run src/agents/merge-risk.ts --message "Apply the merge-risk skill to PR #<n> in <owner/repo>"
```

With `--json`, stdout is a machine envelope; the workflow pipes
`jq -r '.message'` into `gh pr comment --body-file`.

## Observable comparison (pass criteria)

- `flue run` stdout starts with `**Merge risk:` and contains the 5 category
  verdicts (Alignment / Impact / Verification / Rollback cost / Error detection).
- Draft workflow posts that body via `gh pr comment` on PR open.

## Disposal

Delete this dir after the decision, or promote only via a separate approved
spec. This prototype authorizes no merge and no production workflow install.
